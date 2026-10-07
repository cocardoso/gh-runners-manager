package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeController struct{ destroyed []string }

func (f *fakeController) ScaleSets(context.Context) []controller.ScaleSetStatus {
	return []controller.ScaleSetStatus{{Name: "lab", GitHubID: 7, Desired: 1, Live: 1}}
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
	h.srv = httptest.NewServer(New(Deps{Store: db, Recorder: h.rec, Logs: h.logs, Controller: h.ctl, AdminToken: adminToken}))
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
	var ss struct{ ScaleSets []map[string]any `json:"scale_sets"` }
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

type sseEvent struct{ id, data string }

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
