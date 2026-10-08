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
		if name, _ := data["job"].(string); name != "" && e.JobID == "" {
			c.startJobFromAgent(ctx, e, name, at)
		}
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

// startJobFromAgent marks the job the runner just took as running. GitHub's JobStarted
// message lags by tens of seconds, sometimes past the job's end. The runner reports only
// the job's name, so the job is matched only when one queued job of the scale set has it;
// otherwise GitHub's message links it later.
func (c *Controller) startJobFromAgent(ctx context.Context, e store.Environment, name string, at time.Time) {
	queued, err := c.d.Store.ListJobs(ctx, store.JobFilter{ScaleSet: e.ScaleSet, Status: "assigned", Limit: 1000})
	if err != nil {
		return
	}
	var match *store.Job
	for i := range queued {
		if queued[i].DisplayName != name || queued[i].EnvironmentID != "" {
			continue
		}
		if match != nil {
			return
		}
		match = &queued[i]
	}
	if match == nil {
		return
	}
	j := store.Job{ID: match.ID, Status: "running", StartedAt: at, RunnerName: e.RunnerName, EnvironmentID: e.ID}
	if err := c.d.Store.UpsertJob(ctx, j); err != nil {
		return
	}
	_, _ = c.d.Store.UpdateEnvironment(ctx, e.ID, func(x *store.Environment) { x.JobID = match.ID })
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "job.started", Level: "info", Time: at,
		Message:  name + " started on " + e.RunnerName,
		ScaleSet: e.ScaleSet, EnvironmentID: e.ID, JobID: match.ID, Data: map[string]any{"repository": match.Repository, "run_id": match.RunID}})
}
