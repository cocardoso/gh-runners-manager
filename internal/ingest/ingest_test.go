package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeResolver map[string]string

func (f fakeResolver) Resolve(_ context.Context, hash string) (string, bool) {
	id, ok := f[hash]
	return id, ok
}

type fakeSink struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeSink) AgentEvent(_ context.Context, envID, name string, _ time.Time, _ map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, envID+":"+name)
}

type harness struct {
	srv   *httptest.Server
	logs  *logs.Store
	sink  *fakeSink
	token string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	token, _ := NewToken()
	h := &harness{logs: logs.New(t.TempDir(), db), sink: &fakeSink{}, token: token}
	rec := events.NewRecorder(db, events.NewBus(), nil)
	h.srv = httptest.NewServer(NewServer(fakeResolver{HashToken(token): "env1"}, h.sink, h.logs, rec))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) post(t *testing.T, token string, frames ...any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	for _, f := range frames {
		if s, ok := f.(string); ok {
			buf.WriteString(s + "\n")
			continue
		}
		b, _ := json.Marshal(f)
		buf.Write(append(b, '\n'))
	}
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+FramesPath, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func logFrame(stream string, seq int64, text string) Frame {
	return Frame{Type: TypeLog, Stream: stream, Seq: seq, Time: time.Unix(seq, 0), Text: text}
}

func (h *harness) read(t *testing.T, stream string) []string {
	t.Helper()
	entries, _, err := h.logs.Read(context.Background(), "env1", stream, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Text
	}
	return out
}

func TestFramesWithValidTokenAreStored(t *testing.T) {
	h := newHarness(t)
	resp := h.post(t, h.token, logFrame("job", 1, "hello"), logFrame("job", 2, "world"),
		Frame{Type: TypeMetric, Seq: 1, Time: time.Unix(1, 0), CPUUsec: 10, MemBytes: 20})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct{ Accepted int }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Accepted != 3 {
		t.Fatalf("accepted = %d, want 3", body.Accepted)
	}
	if got := h.read(t, "job"); strings.Join(got, ",") != "hello,world" {
		t.Fatalf("job = %v", got)
	}
	if m := h.read(t, "metrics"); len(m) != 1 || !strings.Contains(m[0], `"mem_bytes":20`) {
		t.Fatalf("metrics = %v", m)
	}
}

func TestWrongTokenIsRejectedAndNothingWritten(t *testing.T) {
	h := newHarness(t)
	for _, tok := range []string{"", "nope", strings.Repeat("a", 64)} {
		if resp := h.post(t, tok, logFrame("job", 1, "x")); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", tok, resp.StatusCode)
		}
	}
	if got := h.read(t, "job"); len(got) != 0 {
		t.Fatalf("written with a bad token: %v", got)
	}
}

func TestReplayedFramesAreDeduplicated(t *testing.T) {
	h := newHarness(t)
	h.post(t, h.token, logFrame("runner", 1, "a"), logFrame("runner", 2, "b"), Frame{Type: TypeEvent, Seq: 1, Name: EventHello, Time: time.Unix(1, 0)})
	h.post(t, h.token, logFrame("runner", 1, "a"), logFrame("runner", 2, "b"), logFrame("runner", 3, "c"), Frame{Type: TypeEvent, Seq: 1, Name: EventHello, Time: time.Unix(1, 0)})
	if got := h.read(t, "runner"); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("runner = %v", got)
	}
	if len(h.sink.events) != 1 {
		t.Fatalf("sink events = %v, want the replayed hello delivered once", h.sink.events)
	}
}

func TestMalformedBatchRejectedAtomically(t *testing.T) {
	h := newHarness(t)
	cases := [][]any{
		{logFrame("job", 1, "ok"), "{not json"},
		{logFrame("job", 1, "ok"), logFrame("bogus", 2, "x")},
		{logFrame("job", 1, "ok"), logFrame("control-plane", 2, "agents cannot write here")},
		{logFrame("job", 1, "ok"), Frame{Type: "weird", Seq: 1}},
		{logFrame("job", 0, "seq must be positive")},
		{Frame{Type: TypeEvent, Seq: 1}},
	}
	for i, frames := range cases {
		if resp := h.post(t, h.token, frames...); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("case %d: status = %d, want 400", i, resp.StatusCode)
		}
	}
	if got := h.read(t, "job"); len(got) != 0 {
		t.Fatalf("partially written: %v", got)
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	h := newHarness(t)
	big := logFrame("job", 1, strings.Repeat("x", MaxBodyBytes+1))
	if resp := h.post(t, h.token, big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestEventsReachTheSink(t *testing.T) {
	h := newHarness(t)
	h.post(t, h.token,
		Frame{Type: TypeEvent, Seq: 1, Name: EventHello, Time: time.Unix(1, 0), Data: map[string]any{"version": "dev"}},
		Frame{Type: TypeLog, Stream: "agent", Seq: 2, Text: "starting runner", Time: time.Unix(2, 0)},
		Frame{Type: TypeEvent, Seq: 3, Name: EventRunnerOnline, Time: time.Unix(3, 0)})
	if strings.Join(h.sink.events, ",") != "env1:hello,env1:runner_online" {
		t.Fatalf("sink = %v", h.sink.events)
	}
	if got := h.read(t, "agent"); len(got) != 3 {
		t.Fatalf("agent stream = %v, want events and the log line in order", got)
	}
}

func TestCertificateIsReusedAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := LoadOrCreateCert(dir, []string{"10.50.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := LoadOrCreateCert(dir, []string{"10.50.0.2"})
	if err != nil || fp1 != fp2 || len(fp1) != 95 {
		t.Fatalf("fingerprints %q / %q, %v", fp1, fp2, err)
	}
}

// The agent may give up on a request (timeout) after the server started writing;
// the store and sink phase must finish regardless, or events are lost and lines duplicated.
func TestStoringSurvivesACancelledRequest(t *testing.T) {
	h := newHarness(t)
	var buf bytes.Buffer
	for _, f := range []Frame{logFrame("job", 1, "a"), {Type: TypeEvent, Seq: 1, Name: EventRunnerExited, Time: time.Unix(1, 0)}} {
		b, _ := json.Marshal(f)
		buf.Write(append(b, '\n'))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, FramesPath, &buf).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+h.token)
	sink := &cancelAwareSink{}
	srv := NewServer(fakeResolver{HashToken(h.token): "env1"}, sink, h.logs, nil)
	srv.ServeHTTP(httptest.NewRecorder(), req)
	if got := h.read(t, "job"); len(got) != 1 {
		t.Fatalf("job = %v, want the line stored", got)
	}
	if !sink.liveCtx {
		t.Fatal("the sink must receive a context that is not cancelled")
	}
}

type cancelAwareSink struct{ liveCtx bool }

func (s *cancelAwareSink) AgentEvent(ctx context.Context, _, _ string, _ time.Time, _ map[string]any) {
	s.liveCtx = ctx.Err() == nil
}
