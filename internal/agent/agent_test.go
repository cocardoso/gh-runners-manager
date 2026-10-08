package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	// Sequences are per stream and consecutive, from a base shared by the streams.
	if seqs[0] != seqs[1] || seqs[2] != seqs[1]+1 || seqs[0] <= 0 {
		t.Fatalf("seqs = %v, want agent n, runner n, runner n+1", seqs)
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
	if attr := (Runner{}).sysProcAttr(); attr.Credential != nil || !attr.Setpgid {
		t.Fatal("no credentials when UID is 0, but always a process group")
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

// Final review Critical #1: Run and Flush sending at the same time must not drop frames.
func TestConcurrentRunAndFlushDeliverEverythingOnce(t *testing.T) {
	rec := &recorder{}
	inner := rec.handler(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(time.Now().UnixNano()%40) * time.Millisecond)
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, err := NewClient(Bootstrap{URL: srv.URL, Token: "tok", Fingerprint: ingest.Fingerprint(srv.Certificate().Raw)},
		Options{Interval: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	for i := range 3000 {
		c.Log("job", "line "+strconv.Itoa(i))
	}
	fctx, fcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer fcancel()
	if err := c.Flush(fctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	seen := map[int64]int{}
	var last int64
	for _, f := range rec.frames {
		seen[f.Seq]++
		if f.Seq <= last {
			t.Fatalf("seq %d delivered after %d", f.Seq, last)
		}
		last = f.Seq
	}
	if len(seen) != 3000 {
		t.Fatalf("delivered %d distinct frames, want 3000", len(seen))
	}
}

func TestSequenceNumbersFollowQueueOrder(t *testing.T) {
	c, _ := NewClient(Bootstrap{URL: "https://127.0.0.1:1", Token: "t", Fingerprint: strings.Repeat("AB:", 31) + "AB"}, Options{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				c.Log("agent", "x")
				c.Event("tick", nil)
			}
		}()
	}
	wg.Wait()
	var last int64
	for _, f := range c.pending() {
		if f.Seq <= last {
			t.Fatalf("agent-stream seq %d queued after %d", f.Seq, last)
		}
		last = f.Seq
	}
}

func TestLongLinesAreTruncated(t *testing.T) {
	c, _ := NewClient(Bootstrap{URL: "https://127.0.0.1:1", Token: "t", Fingerprint: strings.Repeat("AB:", 31) + "AB"}, Options{})
	c.Log("job", strings.Repeat("x", 200<<10))
	f := c.pending()[0]
	if len(f.Text) > MaxFrameText+64 || !strings.Contains(f.Text, "truncated") {
		t.Fatalf("frame text is %d bytes, want at most %d with a truncation marker", len(f.Text), MaxFrameText)
	}
}

func TestBatchesAreCappedByBytesAndRejectedBatchesDropped(t *testing.T) {
	rec := &recorder{}
	var mu sync.Mutex
	requests, rejectFirst := 0, true
	inner := rec.handler(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		reject := rejectFirst
		rejectFirst = false
		mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) > 2<<20 {
			t.Errorf("request body %d bytes exceeds the batch byte cap", len(body))
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, _ := NewClient(Bootstrap{URL: srv.URL, Token: "tok", Fingerprint: ingest.Fingerprint(srv.Certificate().Raw)}, Options{Interval: 5 * time.Millisecond})
	c.Log("job", "rejected batch")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("a 400 must drop the batch, not block: %v", err)
	}
	for range 40 {
		c.Log("job", strings.Repeat("y", 60<<10))
	}
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	texts := rec.texts()
	if len(texts) != 41 || texts[0] != "event:frames_rejected" {
		t.Fatalf("delivered %d frames starting with %q; want the rejection event then 40 lines", len(texts), texts[0])
	}
	if requests < 3 {
		t.Fatalf("requests = %d, want the 40 lines split into several byte-capped batches", requests)
	}
}

func TestRunnerHandlesVeryLongLinesAndLingeringChildren(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\nhead -c 3000000 /dev/zero | tr '\\\\0' 'a'\necho\necho \"Listening for Jobs\"\n(sleep 30) &\nexit 0\n"), 0o755)
	var lines []string
	var mu sync.Mutex
	r := Runner{Dir: dir, Script: script, JIT: "x", WaitDelay: 500 * time.Millisecond, OnLine: func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }}
	start := time.Now()
	code, err := r.Run(context.Background())
	if err != nil || code != 0 {
		t.Fatalf("Run = %d, %v", code, err)
	}
	// The child lingers 30 s; writing 3 MB can take several seconds on a loaded machine.
	if time.Since(start) > 15*time.Second {
		t.Fatal("Run waited for a lingering child that kept stdout open")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) < 2 || lines[len(lines)-1] != "Listening for Jobs" || len(lines[0]) > MaxFrameText+64 {
		t.Fatalf("got %d lines (first %d bytes, last %q)", len(lines), len(lines[0]), lines[len(lines)-1])
	}
}

func TestRunnerCancelKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	marker := filepath.Join(dir, "child-alive")
	_ = os.WriteFile(script, []byte("#!/bin/sh\n(sleep 2; touch "+marker+") &\nsleep 30\n"), 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r := Runner{Dir: dir, Script: script, JIT: "x", WaitDelay: 500 * time.Millisecond}
	start := time.Now()
	_, _ = r.Run(ctx)
	if time.Since(start) > 3*time.Second {
		t.Fatal("Run did not return after cancellation")
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a child of run.sh survived cancellation")
	}
}

func TestBootstrapModes(t *testing.T) {
	dir := t.TempDir()
	n := 0
	write := func(kv ...string) string {
		n++
		p := filepath.Join(dir, fmt.Sprint("environ", n))
		_ = os.WriteFile(p, []byte(strings.Join(kv, "\x00")), 0o600)
		return p
	}
	base := []string{ingest.EnvURL + "=https://x", ingest.EnvToken + "=t", ingest.EnvFingerprint + "=" + strings.Repeat("AB", 32)}
	b, ok, err := LoadBootstrap(write(append(base, ingest.EnvMode+"=build")...))
	if err != nil || !ok || b.Mode != "build" {
		t.Fatalf("build mode = %+v %v %v", b, ok, err)
	}
	if _, ok, _ := LoadBootstrap(write(base...)); ok {
		t.Fatal("without a JIT config or a mode the template boot stays idle")
	}
	b, _, _ = LoadBootstrap(write(append(base, ingest.EnvMode+"=selftest", ingest.EnvSelfTestBlocked+"=10.1.1.1:443, 10.1.1.6:8006")...))
	if b.Mode != "selftest" || len(b.Blocked) != 2 || b.Blocked[1] != "10.1.1.6:8006" {
		t.Fatalf("selftest = %+v", b)
	}
}

func TestEnvironmentFileMerge(t *testing.T) {
	p := filepath.Join(t.TempDir(), "environment")
	_ = os.WriteFile(p, []byte("PATH=\"/opt/tool/bin:/usr/bin\"\nImageOS=Linux\n# comment\nLANG=en_US.UTF-8\nbad line\n"), 0o644)
	env := MergeEnvironmentFile([]string{"PATH=/usr/bin", "LANG=C.UTF-8", "HOME=/home/runner"}, p)
	got := strings.Join(env, "|")
	for _, want := range []string{"PATH=/opt/tool/bin:/usr/bin", "ImageOS=Linux", "LANG=C.UTF-8", "HOME=/home/runner"} {
		if !strings.Contains(got, want) {
			t.Errorf("env %s lacks %s", got, want)
		}
	}
	if strings.Contains(got, "en_US") {
		t.Errorf("LANG must stay C.UTF-8: %s", got)
	}
	if strings.Count(got, "PATH=") != 1 {
		t.Errorf("PATH duplicated: %s", got)
	}
}
