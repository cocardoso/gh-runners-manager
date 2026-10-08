package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Reap reconciles the store with the runtime and enforces state timeouts
// (spec §9.1). It is idempotent and safe to run at any time.
func (c *Controller) Reap(ctx context.Context) {
	c.expireQueuedJobs(ctx)
	guests, err := c.d.Runtime.List(ctx)
	if err != nil {
		_, _ = c.d.Recorder.Warn(ctx, "reaper.error", "listing runtime environments failed: "+err.Error(), events.Refs{}, nil)
		return
	}
	byEnv := map[string]runtime.Status{}
	for _, g := range guests {
		row, err := c.d.Store.GetEnvironment(ctx, g.EnvironmentID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && row.State == string(environment.Destroyed)) {
			if derr := c.d.Runtime.Destroy(ctx, g.Ref); derr != nil {
				_, _ = c.d.Recorder.Warn(ctx, "reaper.orphan", fmt.Sprintf("orphan %s could not be destroyed: %v", g.Ref, derr), events.Refs{EnvironmentID: g.EnvironmentID}, nil)
				continue
			}
			_, _ = c.d.Recorder.Warn(ctx, "reaper.orphan", "destroyed orphan environment "+g.Ref.String(), events.Refs{EnvironmentID: g.EnvironmentID}, nil)
			continue
		}
		byEnv[g.EnvironmentID] = g
	}

	live, err := c.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: liveStates})
	if err != nil {
		return
	}
	now := c.now()
	for _, e := range live {
		st := environment.State(e.State)
		g, exists := byEnv[e.ID]
		switch {
		case e.RuntimeRef != "" && !exists && st != environment.Destroying && st != environment.Failed && c.confirmedGone(ctx, runtime.Ref{ID: e.RuntimeRef}):
			c.Fail(ctx, e.ID, "runtime_gone", errors.New("the runtime environment no longer exists"))
		case exists && !g.Running && poweredOffSilently(st) && now.Sub(e.StateChangedAt) > silentPowerOffGrace && !c.liveRunning(ctx, g.Ref):
			c.log(ctx, e.ID, "control-plane", "guest is no longer running; the agent did not report an exit")
			c.advance(ctx, e.ID, environment.Completing, nil)
		case e.Kind != "" && e.Kind != store.KindJob && (st == environment.Connected || st == environment.Idle || st == environment.Running):
			// A build or self-test runs while connected: the template service enforces its timeouts.
		case st == environment.Idle && c.d.Timeouts.Expired(st, e.StateChangedAt, now):
			// No job came: a normal scale-down, not a failure.
			_, _ = c.d.Recorder.Info(ctx, "environment.idle_timeout", "no job arrived; releasing the environment",
				events.Refs{ScaleSet: e.ScaleSet, EnvironmentID: e.ID}, nil)
			c.destroy(ctx, e.ID)
		case c.d.Timeouts.Expired(st, e.StateChangedAt, now):
			c.Fail(ctx, e.ID, "timeout:"+e.State, fmt.Errorf("stayed %s longer than %s", e.State, c.d.Timeouts[st]))
		}
	}
}

// silentPowerOffGrace keeps the reaper from judging a guest that only just changed
// state; hypervisor listings can lag behind a start by several seconds.
const silentPowerOffGrace = 60 * time.Second

func poweredOffSilently(st environment.State) bool {
	switch st {
	case environment.Booting, environment.Connected, environment.Idle, environment.Running:
		return true
	}
	return false
}

// liveRunning asks the runtime for the live state; on any doubt it answers true.
func (c *Controller) liveRunning(ctx context.Context, ref runtime.Ref) bool {
	st, err := c.d.Runtime.Status(ctx, ref)
	if errors.Is(err, runtime.ErrNotFound) {
		return false
	}
	return err != nil || st.Running
}

// confirmedGone checks with a live status call that a guest missing from the
// runtime snapshot is really gone (it may have been created after the snapshot).
func (c *Controller) confirmedGone(ctx context.Context, ref runtime.Ref) bool {
	_, err := c.d.Runtime.Status(ctx, ref)
	return errors.Is(err, runtime.ErrNotFound)
}

// expireQueuedJobs closes jobs queued longer than GitHub keeps a job queued: GitHub has
// canceled them, and a lost message must not leave them queued here for ever.
func (c *Controller) expireQueuedJobs(ctx context.Context) {
	stale, err := c.d.Store.ListJobs(ctx, store.JobFilter{Status: "assigned", QueuedBefore: c.now().Add(-maxQueue), Limit: 1000})
	if err != nil {
		return
	}
	for _, j := range stale {
		if err := c.d.Store.UpsertJob(ctx, store.Job{ID: j.ID, Status: "completed", Result: "canceled", FinishedAt: c.now()}); err != nil {
			continue
		}
		_, _ = c.d.Recorder.Warn(ctx, "job.completed", j.DisplayName+" completed: canceled (queued for more than 24 hours)",
			events.Refs{ScaleSet: j.ScaleSet, JobID: j.ID}, map[string]any{"result": "canceled"})
	}
}
