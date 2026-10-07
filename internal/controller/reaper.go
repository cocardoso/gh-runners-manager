package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Reap reconciles the store with the runtime and enforces state timeouts
// (spec §9.1). It is idempotent and safe to run at any time.
func (c *Controller) Reap(ctx context.Context) {
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
		case e.RuntimeRef != "" && !exists && st != environment.Destroying && st != environment.Failed:
			c.Fail(ctx, e.ID, "runtime_gone", errors.New("the runtime environment no longer exists"))
		case exists && !g.Running && (st == environment.Booting || st == environment.Connected || st == environment.Idle || st == environment.Running):
			c.log(ctx, e.ID, "control-plane", "guest is no longer running; the agent did not report an exit")
			c.advance(ctx, e.ID, environment.Completing, nil)
		case c.d.Timeouts.Expired(st, e.StateChangedAt, now):
			c.Fail(ctx, e.ID, "timeout:"+e.State, fmt.Errorf("stayed %s longer than %s", e.State, c.d.Timeouts[st]))
		}
	}
}
