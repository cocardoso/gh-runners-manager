package controller

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"

	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type scaler struct {
	c    *Controller
	name string
}

// Scaler returns the listener.Scaler for one scale set.
func (c *Controller) Scaler(name string) listener.Scaler { return &scaler{c: c, name: name} }

// HandleDesiredRunnerCount records the demand, reconciles, and reports the serving count.
func (s *scaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	c := s.c
	c.mu.Lock()
	st, ok := c.scaleSets[s.name]
	changed := ok && st.desired != count
	if ok {
		st.desired = count
	}
	c.mu.Unlock()
	if !ok {
		return 0, errors.New("controller: unknown scale set " + s.name)
	}
	if changed {
		_, _ = c.d.Recorder.Info(ctx, "scaleset.demand", "assigned jobs: "+strconv.Itoa(count), events.Refs{ScaleSet: s.name}, map[string]any{"desired": count})
	}
	if err := c.Reconcile(ctx); err != nil {
		_, _ = c.d.Recorder.Error(ctx, "controller.error", "reconcile failed: "+err.Error(), events.Refs{ScaleSet: s.name}, nil)
	}
	envs, err := c.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: servingStates, ScaleSet: s.name})
	if err != nil {
		return 0, err
	}
	return len(envs), nil
}

func (s *scaler) job(base scaleset.JobMessageBase, runner string) store.Job {
	repo := base.RepositoryName
	if base.OwnerName != "" {
		repo = base.OwnerName + "/" + base.RepositoryName
	}
	// GitHub leaves queueTime empty in practice; the scale set assignment time is the
	// moment the job reached this scale set. The store keeps the first queue time recorded.
	queued := base.QueueTime
	if queued.IsZero() {
		queued = base.ScaleSetAssignTime
	}
	return store.Job{ID: base.JobID, ScaleSet: s.name, Repository: repo, Owner: base.OwnerName, WorkflowRef: base.JobWorkflowRef,
		DisplayName: base.JobDisplayName, EventName: base.EventName, RunID: base.WorkflowRunID, RunnerName: runner, QueuedAt: queued}
}

// HandleJobAvailable records a job assigned to the scale set with its queue time,
// which GitHub sends only in this message. It never moves a known job back.
func (s *scaler) HandleJobAvailable(ctx context.Context, info *scaleset.JobAvailable) error {
	c := s.c
	j := s.job(info.JobMessageBase, "")
	for _, t := range []time.Time{info.QueueTime, info.ScaleSetAssignTime, c.now()} {
		if !t.IsZero() {
			j.QueuedAt = t
			break
		}
	}
	existing, err := c.d.Store.GetJob(ctx, j.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		j.Status = "assigned"
	case err != nil:
		return err
	case !existing.QueuedAt.IsZero():
		return nil
	default:
		j = store.Job{ID: j.ID, QueuedAt: j.QueuedAt}
	}
	return c.d.Store.UpsertJob(ctx, j)
}

// HandleJobStarted links the job to its environment and marks it running.
func (s *scaler) HandleJobStarted(ctx context.Context, info *scaleset.JobStarted) error {
	c := s.c
	j := s.job(info.JobMessageBase, info.RunnerName)
	j.Status = "running"
	j.StartedAt = info.RunnerAssignTime
	if j.StartedAt.IsZero() {
		j.StartedAt = c.now()
	}
	envID := ""
	if e, err := c.d.Store.FindEnvironmentByRunner(ctx, info.RunnerName); err == nil {
		envID = e.ID
		j.EnvironmentID = e.ID
		_, _ = c.d.Store.UpdateEnvironment(ctx, e.ID, func(x *store.Environment) { x.JobID = j.ID })
		c.advance(ctx, e.ID, environment.Running, nil)
	}
	// The agent may have claimed the job already: then this message only completes the
	// record. A job that finished before this late message keeps its result, and gets its
	// start event only if nobody recorded one.
	record := true
	if claimed, err := c.d.Store.ClaimJob(ctx, j.ID, envID, info.RunnerName, j.StartedAt); err != nil {
		return err
	} else if !claimed {
		if known, err := c.d.Store.GetJob(ctx, j.ID); err == nil {
			record = known.StartedAt.IsZero()
			if known.Status != "assigned" {
				j.Status = "" // keep running or completed
			}
		}
	}
	if err := c.d.Store.UpsertJob(ctx, j); err != nil {
		return err
	}
	if !record {
		return nil
	}
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "job.started", Level: "info", Time: c.now(),
		Message:  j.DisplayName + " started on " + info.RunnerName,
		ScaleSet: s.name, EnvironmentID: envID, JobID: j.ID, Data: map[string]any{"repository": j.Repository, "run_id": j.RunID}})
	return nil
}

// HandleJobCompleted records the result and moves the environment to completing.
func (s *scaler) HandleJobCompleted(ctx context.Context, info *scaleset.JobCompleted) error {
	c := s.c
	j := s.job(info.JobMessageBase, info.RunnerName)
	j.Status, j.Result = "completed", info.Result
	j.FinishedAt = info.FinishTime
	if j.FinishedAt.IsZero() {
		j.FinishedAt = c.now()
	}
	envID := ""
	if e, err := c.d.Store.FindEnvironmentByRunner(ctx, info.RunnerName); err == nil {
		envID = e.ID
		j.EnvironmentID = e.ID
		c.advance(ctx, e.ID, environment.Completing, nil)
		c.Kick()
	}
	if err := c.d.Store.UpsertJob(ctx, j); err != nil {
		return err
	}
	level := "info"
	if info.Result != "succeeded" {
		level = "warn"
	}
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "job.completed", Level: level, Time: c.now(),
		Message:  j.DisplayName + " completed: " + info.Result,
		ScaleSet: s.name, EnvironmentID: envID, JobID: j.ID, Data: map[string]any{"result": info.Result}})
	return nil
}
