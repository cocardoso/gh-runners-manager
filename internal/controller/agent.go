package controller

import (
	"context"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// AgentEvent implements ingest.EventSink: agent lifecycle events drive the environment state.
func (c *Controller) AgentEvent(ctx context.Context, envID, name string, at time.Time, data map[string]any) {
	e, err := c.d.Store.GetEnvironment(ctx, envID)
	if err != nil {
		return
	}
	level := "info"
	if e.Kind != "" && e.Kind != store.KindJob {
		if name == ingest.EventHello {
			c.advance(ctx, envID, environment.Connected, func(x *store.Environment) {
				if ip, ok := data["ip"].(string); ok {
					x.IP = ip
				}
			})
		}
		if c.d.TemplateEvents != nil {
			c.d.TemplateEvents.AgentEvent(ctx, envID, name, at, data)
		}
		if name == ingest.EventBuildFailed {
			level = "error"
		}
		_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "agent." + name, Level: level, Time: at,
			Message: "agent: " + name, EnvironmentID: envID, Data: data})
		return
	}
	switch name {
	case ingest.EventHello:
		c.advance(ctx, envID, environment.Connected, func(x *store.Environment) {
			if ip, ok := data["ip"].(string); ok {
				x.IP = ip
			}
		})
	case ingest.EventRunnerOnline:
		c.advance(ctx, envID, environment.Idle, nil)
	case ingest.EventJobStarted:
		c.advance(ctx, envID, environment.Running, nil)
	case ingest.EventRunnerExited:
		c.advance(ctx, envID, environment.Completing, func(x *store.Environment) {
			if code, ok := data["exit_code"].(float64); ok {
				v := int(code)
				x.ExitCode = &v
			}
		})
		c.Kick()
	case ingest.EventFramesDropped:
		level = "warn"
	}
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "agent." + name, Level: level, Time: at,
		Message: "agent: " + name, ScaleSet: e.ScaleSet, EnvironmentID: envID, JobID: e.JobID, Data: data})
}
