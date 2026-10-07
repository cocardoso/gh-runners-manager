package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

func TestParseEnviron(t *testing.T) {
	raw := []byte("PATH=/bin\x00GHRM_JITCONFIG=abc==\x00GHRM_ENVIRONMENT_ID=env1\x00GHRM_INGEST_URL=https://x:8443\x00GHRM_INGEST_TOKEN=t\x00GHRM_INGEST_FINGERPRINT=AA\x00")
	path := filepath.Join(t.TempDir(), "environ")
	_ = os.WriteFile(path, raw, 0o600)
	b, ok, err := LoadBootstrap(path)
	if err != nil || !ok {
		t.Fatalf("LoadBootstrap = %v %v", ok, err)
	}
	if b.JITConfig != "abc==" || b.EnvironmentID != "env1" || b.URL != "https://x:8443" || b.Token != "t" || b.Fingerprint != "AA" {
		t.Fatalf("bootstrap = %+v", b)
	}
	_ = os.WriteFile(path, []byte("PATH=/bin\x00"), 0o600)
	if _, ok, err := LoadBootstrap(path); ok || err != nil {
		t.Fatalf("without JIT config: ok=%v err=%v, want ok=false (template boot)", ok, err)
	}
}

type recorder struct {
	mu     sync.Mutex
	frames []ingest.Frame
	fail   int
}

func (r *recorder) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.fail > 0 {
			r.fail--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		sc := bufio.NewScanner(req.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var f ingest.Frame
			if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
				t.Errorf("bad frame: %v", err)
			}
			r.frames = append(r.frames, f)
		}
		_, _ = w.Write([]byte(`{"accepted":1}`))
	})
}

func (r *recorder) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, f := range r.frames {
		if f.Type == ingest.TypeLog {
			out = append(out, f.Text)
		} else {
			out = append(out, f.Type+":"+f.Name)
		}
	}
	return out
}

func TestClientPinsFingerprintAndRetries(t *testing.T) {
	rec := &recorder{fail: 1}
	srv := httptest.NewTLSServer(rec.handler(t))
	defer srv.Close()
	fp := ingest.Fingerprint(srv.Certificate().Raw)

	bad, err := NewClient(Bootstrap{URL: srv.URL, Token: "tok", Fingerprint: strings.Repeat("AB:", 31) + "AB"}, Options{Interval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	bad.Log("agent", "never delivered")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	if err := bad.Flush(ctx); err == nil {
		t.Fatal("a wrong fingerprint must not deliver")
	}
	cancel()

	c, err := NewClient(Bootstrap{URL: srv.URL, Token: "tok", Fingerprint: fp}, Options{Interval: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.Event(ingest.EventHello, map[string]any{"v": 1})
	c.Log("runner", "one")
	c.Log("runner", "two")
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.texts(), ","); got != "event:hello,one,two" {
		t.Fatalf("delivered %q, want in order after one 503", got)
	}
	rec.mu.Lock()
	seqs := []int64{rec.frames[0].Seq, rec.frames[1].Seq, rec.frames[2].Seq}
	rec.mu.Unlock()
	if seqs[0] != 1 || seqs[1] != 1 || seqs[2] != 2 {
		t.Fatalf("seqs = %v, want agent 1, runner 1, runner 2", seqs)
	}
}

func TestQueueCapDropsOldestLogsAndReports(t *testing.T) {
	c, err := NewClient(Bootstrap{URL: "https://127.0.0.1:1", Token: "tok", Fingerprint: strings.Repeat("AB:", 31) + "AB"}, Options{MaxQueue: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		c.Log("runner", s)
	}
	q := c.pending()
	if len(q) != 3 || q[0].Text != "c" || c.Dropped() != 2 {
		t.Fatalf("queue = %+v dropped %d", q, c.Dropped())
	}
}

func TestTailFollowsNewFilesAndPartialLines(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "pages"), 0o755)
	var mu sync.Mutex
	var got []string
	tl := NewTailer(dir, map[string]string{"Runner_*.log": "runner", "pages/*.log": "job"}, func(stream, line string) {
		mu.Lock()
		got = append(got, stream+":"+line)
		mu.Unlock()
	})
	_ = os.WriteFile(filepath.Join(dir, "Runner_1.log"), []byte("r1\nr2 part"), 0o644)
	tl.Poll()
	f, _ := os.OpenFile(filepath.Join(dir, "Runner_1.log"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("ial\n")
	_ = f.Close()
	_ = os.WriteFile(filepath.Join(dir, "pages", "abc_1.log"), []byte("step output\n"), 0o644)
	tl.Poll()
	tl.Poll()
	if strings.Join(got, ",") != "runner:r1,runner:r2 partial,job:step output" {
		t.Fatalf("got %v", got)
	}
}

func TestRunnerEventsFromOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho \"args: $*\"\necho \"√ Connected to GitHub\"\necho \"Listening for Jobs\"\necho \"Running job: build\" 1>&2\necho \"Job build completed with result: Succeeded\"\nexit 3\n"), 0o755)
	var lines []string
	var mu sync.Mutex
	r := Runner{Dir: dir, Script: script, JIT: "abc", OnLine: func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }}
	code, err := r.Run(context.Background())
	if err != nil || code != 3 {
		t.Fatalf("Run = %d, %v; want exit code 3", code, err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "args: --jitconfig abc") || !strings.Contains(joined, "Running job: build") {
		t.Fatalf("lines = %q", joined)
	}
	cases := map[string]string{
		"Listening for Jobs":                                               ingest.EventRunnerOnline,
		"2026-10-07 12:00:00Z: Running job: build":                         ingest.EventJobStarted,
		"2026-10-07 12:00:05Z: Job build completed with result: Succeeded": ingest.EventJobFinished,
		"something else":                                                   "",
	}
	for line, want := range cases {
		if got, _ := ClassifyLine(line); got != want {
			t.Errorf("ClassifyLine(%q) = %q, want %q", line, got, want)
		}
	}
	if _, data := ClassifyLine("Job build completed with result: Failed"); data["result"] != "Failed" {
		t.Errorf("result = %v", data)
	}
}

func TestMetricsReadsCgroupFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte("usage_usec 12345\nuser_usec 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "memory.current"), []byte("4096\n"), 0o644)
	cpu, mem, err := ReadCgroup(dir)
	if err != nil || cpu != 12345 || mem != 4096 {
		t.Fatalf("ReadCgroup = %d %d %v", cpu, mem, err)
	}
	if _, _, err := ReadCgroup(t.TempDir()); err == nil {
		t.Fatal("want an error for a missing cgroup")
	}
}

func TestRunnerCredentialIncludesSupplementaryGroups(t *testing.T) {
	r := Runner{UID: 1001, GID: 1001, Groups: []uint32{1001, 999}}
	attr := r.sysProcAttr()
	if attr == nil || attr.Credential == nil {
		t.Fatal("want credentials when UID is set")
	}
	if attr.Credential.Uid != 1001 || len(attr.Credential.Groups) != 2 || attr.Credential.Groups[1] != 999 {
		t.Fatalf("credential = %+v, want the docker group (999) among the supplementary groups", attr.Credential)
	}
	if (Runner{}).sysProcAttr() != nil {
		t.Fatal("no credentials when UID is 0")
	}
}

func TestJobRecordIDFromDiag(t *testing.T) {
	id, ok := JobRecordID("[2026-10-07 14:54:47Z INFO JobDispatcher] Job request 0 for plan 5004-aa job 5ad26d73-2db2-5572-b9df-81f5f8ae7fb3 received.")
	if !ok || id != "5ad26d73-2db2-5572-b9df-81f5f8ae7fb3" {
		t.Fatalf("JobRecordID = %q %v", id, ok)
	}
	if _, ok := JobRecordID("[INFO JobRunner] something else"); ok {
		t.Fatal("unexpected match")
	}
}

func TestTailerAcceptFilterAndNumericPageOrder(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "pages"), 0o755)
	var got []string
	jobID := ""
	tl := NewTailer(dir, map[string]string{"pages/*.log": "job"}, func(stream, line string) { got = append(got, line) })
	tl.Accept = func(stream, path string) bool {
		return jobID != "" && strings.Contains(filepath.Base(path), "_"+jobID+"_")
	}
	write := func(name, content string) {
		_ = os.WriteFile(filepath.Join(dir, "pages", name), []byte(content), 0o644)
	}
	write("plan_step1_1.log", "step line\n")
	write("plan_job9_1.log", "job page 1\n")
	tl.Poll()
	if len(got) != 0 {
		t.Fatalf("read %v before the job id was known", got)
	}
	jobID = "job9"
	write("plan_job9_10.log", "job page 10\n")
	write("plan_job9_2.log", "job page 2\n")
	tl.Poll()
	if strings.Join(got, ",") != "job page 1,job page 2,job page 10" {
		t.Fatalf("got %v, want only the job log, pages in numeric order", got)
	}
}
