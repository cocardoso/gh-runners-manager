package controller

import (
	"context"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func warm(n int) func(*config.Config) {
	return func(c *config.Config) { c.ScaleSets[0].WarmRunners = n }
}

func (h *harness) online(t *testing.T, e store.Environment) {
	t.Helper()
	h.c.AgentEvent(context.Background(), e.ID, ingest.EventHello, time.Now(), nil)
	h.c.AgentEvent(context.Background(), e.ID, ingest.EventRunnerOnline, time.Now(), nil)
}

func TestWarmRunnersAreReadyBeforeAnyJob(t *testing.T) {
	h := newHarness(t, warm(1))
	es := h.provision(t, 0)
	if len(es) != 1 {
		t.Fatalf("environments with no job queued = %d, want the 1 warm runner", len(es))
	}
	// A job takes the warm runner: another one is prepared next to it.
	h.online(t, es[0])
	h.c.AgentEvent(context.Background(), es[0].ID, ingest.EventJobStarted, time.Now(), nil)
	if got := h.provision(t, 1); len(got) != 1 {
		t.Fatalf("booting after the warm runner was taken = %d, want 1 new warm runner", len(got))
	}
}

func TestWarmRunnersStayWithinTheScaleSetLimit(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.ScaleSets[0].WarmRunners = 2; c.ScaleSets[0].MaxConcurrent = 3 })
	if es := h.provision(t, 2); len(es) != 3 {
		t.Fatalf("environments for 2 jobs + 2 warm with a limit of 3 = %d, want 3", len(es))
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{Limit: 1000})
	for _, ev := range evs {
		if ev.Kind == "scaleset.waiting" {
			t.Fatalf("a warm runner the limit leaves out is not a waiting job: %s", ev.Message)
		}
	}
}

func TestWarmRunnersBlockedByCapacityAreNotWaitingJobs(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.ScaleSets[0].WarmRunners = 1; c.Capacity.MaxEnvironments = 0 })
	h.provision(t, 0)
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{Limit: 1000})
	for _, ev := range evs {
		if ev.Kind == "scaleset.waiting" {
			t.Fatalf("no job waits, but got %s", ev.Message)
		}
	}
	for _, s := range h.c.ScaleSets(context.Background()) {
		if s.Waiting != "" {
			t.Fatalf("scale set %s waiting = %q, want none", s.Name, s.Waiting)
		}
	}
}

func TestIdleWarmRunnerOutlivesTheIdleTimeoutForAnHour(t *testing.T) {
	h := newHarness(t, warm(1))
	ctx := context.Background()
	e := h.provision(t, 0)[0]
	h.online(t, e)
	h.now = h.now.Add(30 * time.Minute)
	h.c.Reap(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "idle" {
		t.Fatalf("warm runner after 30 min = %s, want idle", got.State)
	}
	// After an hour it is replaced, so it picks up a newer template.
	h.now = h.now.Add(31 * time.Minute)
	h.c.Reap(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "destroyed" {
		t.Fatalf("warm runner after 61 min = %s, want destroyed", got.State)
	}
}

func TestSurplusIdleRunnersStillTimeOut(t *testing.T) {
	h := newHarness(t, warm(1))
	ctx := context.Background()
	es := h.provision(t, 1) // one for the job, one warm
	if len(es) != 2 {
		t.Fatalf("environments = %d, want 2", len(es))
	}
	for _, e := range es {
		h.online(t, e)
	}
	// The job was canceled: two idle runners, one more than the warm pool.
	if _, err := h.c.Scaler("lab").HandleDesiredRunnerCount(ctx, 0); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(11 * time.Minute)
	h.c.Reap(ctx)
	if idle := h.envs(t, "idle"); len(idle) != 1 {
		t.Fatalf("idle after the timeout = %d, want the 1 warm runner", len(idle))
	}
}
