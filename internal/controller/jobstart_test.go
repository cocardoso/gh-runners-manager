package controller

import (
	"context"
	"testing"
	"time"

	"github.com/actions/scaleset"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func (h *harness) events(t *testing.T, kind string) []store.Event {
	t.Helper()
	evs, err := h.db.ListEvents(context.Background(), store.EventFilter{Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestGitHubFirstThenAgentRecordsOneStart(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	sc := fullScaler(h.c.Scaler("lab"))
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: jobBase("j1", "")})
	_ = sc.HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: e.RunnerName, JobMessageBase: jobBase("j1", e.RunnerName)})
	h.c.AgentEvent(ctx, e.ID, ingest.EventJobStarted, time.Now(), map[string]any{"job": "build"})
	if n := len(h.events(t, "job.started")); n != 1 {
		t.Fatalf("job.started events = %d, want 1", n)
	}
	if j, _ := h.db.GetJob(ctx, "j1"); j.Status != "running" || j.EnvironmentID != e.ID {
		t.Fatalf("job = %+v", j)
	}
}

func TestAgentEventCarriesTheJobItLinked(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	_ = fullScaler(h.c.Scaler("lab")).HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: jobBase("j1", "")})
	h.c.AgentEvent(ctx, e.ID, ingest.EventJobStarted, time.Now(), map[string]any{"job": "build"})
	evs := h.events(t, "agent.job_started")
	if len(evs) != 1 || evs[0].JobID != "j1" {
		t.Fatalf("agent.job_started = %+v, want it filed under j1", evs)
	}
}

func TestAgentTakesOnlyItsScaleSetsRecentJob(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.ScaleSets = append(c.ScaleSets, config.ScaleSet{Name: "other", URL: "https://github.com/o/x", Credential: "c", MaxConcurrent: 1, Cores: 1, MemoryMB: 512})
	})
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	// Same name, other scale set: not ours.
	_ = fullScaler(h.c.Scaler("other")).HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: jobBase("theirs", "")})
	// Same name, queued two days ago and never closed: GitHub canceled it long ago.
	stale := jobBase("stale", "")
	stale.QueueTime = time.Now().Add(-48 * time.Hour)
	_ = fullScaler(h.c.Scaler("lab")).HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: stale})
	_ = fullScaler(h.c.Scaler("lab")).HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: jobBase("mine", "")})
	h.c.AgentEvent(ctx, e.ID, ingest.EventJobStarted, time.Now(), map[string]any{"job": "build"})
	if j, _ := h.db.GetJob(ctx, "mine"); j.Status != "running" {
		t.Fatalf("mine = %s, want running", j.Status)
	}
	for _, id := range []string{"theirs", "stale"} {
		if j, _ := h.db.GetJob(ctx, id); j.Status != "assigned" {
			t.Fatalf("%s = %s, want assigned", id, j.Status)
		}
	}
}

// An organization scale set can queue jobs of the same name from two repositories.
func TestSameNameFromTwoRepositoriesWaitsForGitHub(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	sc := fullScaler(h.c.Scaler("lab"))
	a, b := jobBase("a", ""), jobBase("b", "")
	b.RepositoryName = "other-repo"
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: a})
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: b})
	h.c.AgentEvent(ctx, e.ID, ingest.EventJobStarted, time.Now(), map[string]any{"job": "build"})
	if n := len(h.events(t, "job.started")); n != 0 {
		t.Fatalf("job.started events = %d, want 0 until GitHub says which", n)
	}
}

func TestJobFinishedBeforeItsLateStartGetsOneStartEvent(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	sc := fullScaler(h.c.Scaler("lab"))
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: jobBase("j1", "")})
	_ = sc.HandleJobCompleted(ctx, &scaleset.JobCompleted{Result: "succeeded", RunnerName: e.RunnerName, JobMessageBase: jobBase("j1", e.RunnerName)})
	assigned := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	late := jobBase("j1", e.RunnerName)
	late.RunnerAssignTime = assigned
	_ = sc.HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: e.RunnerName, JobMessageBase: late})
	j, _ := h.db.GetJob(ctx, "j1")
	if j.Status != "completed" || !j.StartedAt.Equal(assigned) {
		t.Fatalf("job = %+v, want completed with its start time", j)
	}
	if n := len(h.events(t, "job.started")); n != 1 {
		t.Fatalf("job.started events = %d, want 1", n)
	}
}

func TestJobsQueuedPastGitHubsLimitAreClosed(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	old := jobBase("old", "")
	old.QueueTime = h.now.Add(-25 * time.Hour)
	_ = fullScaler(h.c.Scaler("lab")).HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: old})
	_ = fullScaler(h.c.Scaler("lab")).HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: jobBase("fresh", "")})
	h.c.Reap(ctx)
	if j, _ := h.db.GetJob(ctx, "old"); j.Status != "completed" || j.Result != "canceled" {
		t.Fatalf("old = %s/%s, want completed/canceled", j.Status, j.Result)
	}
	if j, _ := h.db.GetJob(ctx, "fresh"); j.Status != "assigned" {
		t.Fatalf("fresh = %s, want assigned", j.Status)
	}
}
