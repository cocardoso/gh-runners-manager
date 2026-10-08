package controller

import (
	"context"
	"errors"
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
		if job, _ := data["job"].(string); job != "" && e.JobID == "" {
			if id := c.startJobFromAgent(ctx, e, job, at); id != "" {
				e.JobID = id
			}
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
	case ingest.EventFirewallOpen:
		level = "error"
		reason, _ := data["error"].(string)
		defer c.Fail(ctx, envID, "firewall", errors.New("the job network firewall did not apply to the guest: "+reason))
	}
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "agent." + name, Level: level, Time: at,
		Message: "agent: " + name, ScaleSet: e.ScaleSet, EnvironmentID: envID, JobID: e.JobID, Data: data})
}

// startJobFromAgent marks the job the runner just took as running and returns its ID.
// GitHub's JobStarted message lags by tens of seconds, sometimes past the job's end. The
// runner reports only the job's name, so the job is matched only when one job of the scale
// set with that name is queued (in the last 24 hours, GitHub's queue limit) and on no
// environment; otherwise GitHub's message links it later. The claim is atomic, so the
// agent and GitHub's message never both record the start.
func (c *Controller) startJobFromAgent(ctx context.Context, e store.Environment, job string, at time.Time) string {
	queued, err := c.d.Store.ListJobs(ctx, store.JobFilter{ScaleSet: e.ScaleSet, Status: "assigned", DisplayName: job,
		QueuedAfter: at.Add(-maxQueue), Limit: 2})
	if err != nil || len(queued) != 1 || queued[0].EnvironmentID != "" {
		return ""
	}
	match := queued[0]
	if claimed, err := c.d.Store.ClaimJob(ctx, match.ID, e.ID, e.RunnerName, at); err != nil || !claimed {
		return ""
	}
	_, _ = c.d.Store.UpdateEnvironment(ctx, e.ID, func(x *store.Environment) { x.JobID = match.ID })
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "job.started", Level: "info", Time: at,
		Message:  job + " started on " + e.RunnerName,
		ScaleSet: e.ScaleSet, EnvironmentID: e.ID, JobID: match.ID, Data: map[string]any{"repository": match.Repository, "run_id": match.RunID}})
	return match.ID
}

// maxQueue is how long GitHub keeps a job queued before canceling it.
const maxQueue = 24 * time.Hour
