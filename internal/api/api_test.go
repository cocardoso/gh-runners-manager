package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/github"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeController struct{ destroyed []string }

func (f *fakeController) ScaleSets(context.Context) []controller.ScaleSetStatus {
	return []controller.ScaleSetStatus{{Name: "lab", GitHubID: 7, Desired: 1, Live: 1}}
}

type fakeGitHubJobs struct{}

func (fakeGitHubJobs) JobDetails(_ context.Context, scaleSet, repo string, runID int64, runner string) (github.JobDetails, error) {
	return github.JobDetails{Available: true, ID: 2, URL: "https://github.com/" + repo, Steps: []github.Step{{Number: 1, Name: "build"}}}, nil
}

func (f *fakeController) RequestDestroy(_ context.Context, id string) error {
	f.destroyed = append(f.destroyed, id)
	return nil
}

type harness struct {
	srv  *httptest.Server
	db   *store.Store
	rec  *events.Recorder
	logs *logs.Store
	ctl  *fakeController
}

func newHarness(t *testing.T, adminToken string) *harness {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{db: db, rec: events.NewRecorder(db, events.NewBus(), nil), logs: logs.New(t.TempDir(), db), ctl: &fakeController{}}
	cfg := &config.Config{Proxmox: config.Proxmox{URL: "https://pve.example.test:8006", TokenSecret: "pve-secret"},
		GitHub:    config.GitHub{Credentials: []config.Credential{{Name: "c", Token: "token-value"}}},
		ScaleSets: []config.ScaleSet{{Name: "lab", URL: "https://github.com/o/r", Credential: "c"}}, AdminToken: adminToken}
	h.srv = httptest.NewServer(New(Deps{Store: db, Recorder: h.rec, Logs: h.logs, Controller: h.ctl, AdminToken: adminToken,
		Config: cfg, GitHubJobs: fakeGitHubJobs{}}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) getJSON(t *testing.T, path string, out any) int {
	t.Helper()
	resp, err := http.Get(h.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestListAndGetEndpoints(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "env1", ScaleSet: "lab", State: "running", RunnerName: "ghrm-1", MemoryMB: 512})
	_ = h.db.UpsertJob(ctx, store.Job{ID: "j1", ScaleSet: "lab", Repository: "o/r", Status: "running", EnvironmentID: "env1"})
	_, _ = h.rec.Info(ctx, "k", "hello", events.Refs{EnvironmentID: "env1"}, nil)
	_ = h.logs.Write(ctx, "env1", "control-plane", "created", time.Now())

	var envs struct{ Environments []map[string]any }
	if code := h.getJSON(t, "/api/v1/environments?state=running,idle", &envs); code != 200 || len(envs.Environments) != 1 || envs.Environments[0]["id"] != "env1" {
		t.Fatalf("environments = %d %+v", code, envs)
	}
	var env map[string]any
	if code := h.getJSON(t, "/api/v1/environments/env1", &env); code != 200 || env["state"] != "running" {
		t.Fatalf("environment = %d %+v", code, env)
	}
	if streams, _ := env["log_streams"].([]any); len(streams) != 1 {
		t.Fatalf("log_streams = %+v", env["log_streams"])
	}
	if code := h.getJSON(t, "/api/v1/environments/nope", nil); code != 404 {
		t.Fatalf("missing environment = %d, want 404", code)
	}
	var jobs struct{ Jobs []map[string]any }
	if h.getJSON(t, "/api/v1/jobs", &jobs); len(jobs.Jobs) != 1 || jobs.Jobs[0]["repository"] != "o/r" {
		t.Fatalf("jobs = %+v", jobs)
	}
	var job map[string]any
	if code := h.getJSON(t, "/api/v1/jobs/j1", &job); code != 200 || job["environment_id"] != "env1" {
		t.Fatalf("job = %d %+v", code, job)
	}
	var evs struct{ Events []map[string]any }
	if h.getJSON(t, "/api/v1/events?environment=env1", &evs); len(evs.Events) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	var ss struct {
		ScaleSets []map[string]any `json:"scale_sets"`
	}
	if h.getJSON(t, "/api/v1/scale-sets", &ss); len(ss.ScaleSets) != 1 || ss.ScaleSets[0]["name"] != "lab" {
		t.Fatalf("scale sets = %+v", ss)
	}
	var page struct {
		Entries []map[string]any
		Next    int64
	}
	if h.getJSON(t, "/api/v1/environments/env1/logs/control-plane", &page); len(page.Entries) != 1 || page.Next == 0 {
		t.Fatalf("log page = %+v", page)
	}
	if code := h.getJSON(t, "/api/v1/environments/env1/logs/bogus", nil); code != 422 {
		t.Fatalf("bogus stream = %d, want 422 (validation error)", code)
	}
}

type sseEvent struct{ id, event, data string }

func readSSE(t *testing.T, r *bufio.Reader, n int) []sseEvent {
	t.Helper()
	var out []sseEvent
	cur := sseEvent{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for len(out) < n {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.data != "":
				out = append(out, cur)
				cur = sseEvent{}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after %d of %d events", len(out), n)
	}
	return out
}

func TestEventStreamResumesFromLastEventID(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	for _, m := range []string{"one", "two", "three"} {
		_, _ = h.rec.Info(ctx, "k", m, events.Refs{}, nil)
	}
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/events/stream", nil)
	req.Header.Set("Last-Event-ID", "1")
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(cctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	got := readSSE(t, r, 2)
	if got[0].id != "2" || got[1].id != "3" || !strings.Contains(got[1].data, `"three"`) {
		t.Fatalf("backlog = %+v", got)
	}
	_, _ = h.rec.Info(ctx, "k", "four", events.Refs{}, nil)
	live := readSSE(t, r, 1)
	if live[0].id != "4" {
		t.Fatalf("live = %+v", live)
	}
}

func TestLogFollowStreamsNewLines(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "env1", ScaleSet: "lab", State: "running"})
	_ = h.logs.Write(ctx, "env1", "job", "first", time.Now())
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, h.srv.URL+"/api/v1/environments/env1/logs/job?follow=true", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if got := readSSE(t, r, 1); !strings.Contains(got[0].data, "first") {
		t.Fatalf("backlog = %+v", got)
	}
	_ = h.logs.Write(ctx, "env1", "job", "second", time.Now())
	if got := readSSE(t, r, 1); !strings.Contains(got[0].data, "second") {
		t.Fatalf("live = %+v", got)
	}
}

func post(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestDestroyRequiresAdminToken(t *testing.T) {
	disabled := newHarness(t, "")
	if code := post(t, disabled.srv.URL+"/api/v1/environments/env1/destroy", "x"); code != 403 {
		t.Fatalf("no admin token configured: %d, want 403", code)
	}
	h := newHarness(t, "s3cret")
	_ = h.db.CreateEnvironment(context.Background(), store.Environment{ID: "env1", ScaleSet: "lab", State: "running"})
	if code := post(t, h.srv.URL+"/api/v1/environments/env1/destroy", "wrong"); code != 401 {
		t.Fatalf("wrong token: %d, want 401", code)
	}
	if code := post(t, h.srv.URL+"/api/v1/environments/env1/destroy", "s3cret"); code != 202 {
		t.Fatalf("right token: %d, want 202", code)
	}
	if len(h.ctl.destroyed) != 1 {
		t.Fatalf("destroy not requested: %v", h.ctl.destroyed)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{EnvironmentID: "env1"})
	if len(evs) == 0 || evs[len(evs)-1].Kind != "audit.destroy" {
		t.Fatalf("want an audit.destroy event, got %+v", evs)
	}
}

func TestOpenAPIDocumentIsServed(t *testing.T) {
	h := newHarness(t, "")
	resp, err := http.Get(h.srv.URL + "/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "/api/v1/environments") {
		t.Fatalf("openapi = %d %.200s", resp.StatusCode, body)
	}
}

func TestHealthz(t *testing.T) {
	h := newHarness(t, "")
	if code := h.getJSON(t, "/healthz", nil); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
}

func TestLogFollowRejectsUnknownEnvironment(t *testing.T) {
	h := newHarness(t, "")
	resp, err := http.Get(h.srv.URL + "/api/v1/environments/missing/logs/job?follow=true")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404 for an unknown environment", resp.StatusCode)
	}
	resp, _ = http.Get(h.srv.URL + "/api/v1/environments/env1/logs/bogus?follow=true")
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 for an unknown stream", resp.StatusCode)
	}
}

func TestLogFollowEndsForDestroyedEnvironment(t *testing.T) {
	followIdleCheck = 50 * time.Millisecond
	defer func() { followIdleCheck = 5 * time.Second }()
	h := newHarness(t, "")
	ctx := context.Background()
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "env1", ScaleSet: "lab", State: "destroyed"})
	_ = h.logs.Write(ctx, "env1", "job", "last line", time.Now())
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Get(h.srv.URL + "/api/v1/environments/env1/logs/job?follow=true")
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("following a destroyed environment's log never ended")
	}
}

func TestOverviewAndStats(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	now := time.Now()
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "e1", ScaleSet: "lab", State: "running", MemoryMB: 2048})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "e2", ScaleSet: "lab", State: "destroyed", MemoryMB: 2048, FailureStage: "create", FailureReason: "boom"})
	_ = h.db.UpsertJob(ctx, store.Job{ID: "j1", ScaleSet: "lab", Status: "completed", Result: "succeeded", QueuedAt: now.Add(-10 * time.Minute), StartedAt: now.Add(-9 * time.Minute), FinishedAt: now.Add(-5 * time.Minute)})
	_ = h.db.UpsertJob(ctx, store.Job{ID: "j2", ScaleSet: "lab", Status: "completed", Result: "failed", QueuedAt: now.Add(-4 * time.Minute), StartedAt: now.Add(-3 * time.Minute), FinishedAt: now.Add(-time.Minute)})
	_ = h.db.UpsertJob(ctx, store.Job{ID: "j3", ScaleSet: "lab", Status: "running", QueuedAt: now.Add(-time.Minute), StartedAt: now})
	var ov map[string]any
	if code := h.getJSON(t, "/api/v1/overview", &ov); code != 200 {
		t.Fatalf("overview = %d", code)
	}
	k := ov["kpis"].(map[string]any)
	if k["running_jobs"] != 1.0 || k["jobs_24h"] != 2.0 || k["success_rate_24h"] != 0.5 || k["median_queue_seconds_24h"] != 60.0 {
		t.Fatalf("kpis = %+v", k)
	}
	c := ov["capacity"].(map[string]any)
	if c["environments_live"] != 1.0 || c["memory_committed_mb"] != 2048.0 {
		t.Fatalf("capacity = %+v", c)
	}
	if alerts, _ := ov["alerts"].([]any); len(alerts) == 0 {
		t.Fatalf("alerts = %+v, want the failed environment", ov["alerts"])
	}
	var st struct{ Buckets []map[string]any }
	if code := h.getJSON(t, "/api/v1/stats/jobs?hours=24", &st); code != 200 || len(st.Buckets) != 24 {
		t.Fatalf("stats = %d %d buckets", code, len(st.Buckets))
	}
	total := 0.0
	for _, b := range st.Buckets {
		total += b["succeeded"].(float64) + b["failed"].(float64)
	}
	if total != 2 {
		t.Fatalf("bucketed jobs = %v, want 2", total)
	}
}

func TestSettingsHaveNoSecrets(t *testing.T) {
	h := newHarness(t, "s3cret")
	var s map[string]any
	if code := h.getJSON(t, "/api/v1/settings", &s); code != 200 {
		t.Fatalf("settings = %d", code)
	}
	raw, _ := json.Marshal(s)
	for _, secret := range []string{"s3cret", "token-value", "pve-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("settings leak %q: %s", secret, raw)
		}
	}
	if s["admin_actions"] != true || s["version"] == "" {
		t.Fatalf("settings = %s", raw)
	}
}

func TestJobGitHubDetails(t *testing.T) {
	h := newHarness(t, "")
	_ = h.db.UpsertJob(context.Background(), store.Job{ID: "j1", ScaleSet: "lab", Repository: "o/r", RunID: 7, RunnerName: "ghrm-abc"})
	var d map[string]any
	if code := h.getJSON(t, "/api/v1/jobs/j1/github", &d); code != 200 || d["available"] != true {
		t.Fatalf("github = %d %+v", code, d)
	}
	if code := h.getJSON(t, "/api/v1/jobs/nope/github", nil); code != 404 {
		t.Fatalf("missing job = %d", code)
	}
}

func TestEventsNewestPageAndLatestStream(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	for _, m := range []string{"one", "two", "three", "four"} {
		_, _ = h.rec.Info(ctx, "k", m, events.Refs{}, nil)
	}
	var page struct{ Events []store.Event }
	h.getJSON(t, "/api/v1/events?newest=true&limit=2", &page)
	if len(page.Events) != 2 || page.Events[0].Seq != 3 || page.Events[1].Seq != 4 {
		t.Fatalf("newest = %+v", page.Events)
	}
	h.getJSON(t, "/api/v1/events?newest=true&before=3&limit=5", &page)
	if len(page.Events) != 2 || page.Events[0].Seq != 1 {
		t.Fatalf("before 3 = %+v", page.Events)
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, h.srv.URL+"/api/v1/events/stream?after=latest", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	time.Sleep(50 * time.Millisecond) // let the stream start before the next event
	_, _ = h.rec.Info(ctx, "k", "five", events.Refs{}, nil)
	if got := readSSE(t, r, 1); got[0].id != "5" {
		t.Fatalf("after=latest delivered %+v, want only the new event 5", got)
	}
}

func TestEventStreamSendsNamedHeartbeats(t *testing.T) {
	heartbeat = 30 * time.Millisecond
	defer func() { heartbeat = 15 * time.Second }()
	h := newHarness(t, "")
	cctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, h.srv.URL+"/api/v1/events/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := readSSE(t, bufio.NewReader(resp.Body), 1)
	if got[0].event != "ping" {
		t.Fatalf("heartbeat = %+v, want a named ping event the browser can observe", got)
	}
}

func TestLogTailPage(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "env1", ScaleSet: "lab", State: "running"})
	for _, m := range []string{"a", "b", "c"} {
		_ = h.logs.Write(ctx, "env1", "job", m, time.Now())
	}
	var page struct {
		Entries []logs.Entry
		Next    int64
	}
	h.getJSON(t, "/api/v1/environments/env1/logs/job?tail=true&limit=2", &page)
	if len(page.Entries) != 2 || page.Entries[0].Text != "b" || page.Entries[1].Text != "c" || page.Next == 0 {
		t.Fatalf("tail = %+v", page)
	}
	h.getJSON(t, fmt.Sprintf("/api/v1/environments/env1/logs/job?tail=true&before=%d&limit=10", page.Entries[0].Offset), &page)
	if len(page.Entries) != 1 || page.Entries[0].Text != "a" {
		t.Fatalf("before b = %+v", page)
	}
}
