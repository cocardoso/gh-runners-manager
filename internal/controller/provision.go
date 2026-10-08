package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/ids"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/scheduler"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// servingStates are states of environments that serve, or will serve, an assigned job.
// completing is included: GitHub lowers the assigned-job count only after the job
// completes, so counting it avoids provisioning a replacement for a finished job.
var servingStates = []string{"pending", "provisioning", "booting", "connected", "idle", "running", "completing"}

// liveStates hold host resources.
var liveStates = []string{"pending", "provisioning", "booting", "connected", "idle", "running", "completing", "failed", "destroying"}

const provisionTimeout = 5 * time.Minute

// Reconcile creates environments for unmet demand within the capacity limits.
func (c *Controller) Reconcile(ctx context.Context) error {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()

	live, err := c.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: liveStates})
	if err != nil {
		return err
	}
	serving := map[string]int{}
	committed := 0
	for _, e := range live {
		committed += e.MemoryMB
		if isServing(e.State) {
			serving[e.ScaleSet]++
		}
	}

	c.mu.Lock()
	var demands []scheduler.Demand
	assigned := map[string]int{}
	now := c.now()
	for _, name := range c.order {
		s := c.scaleSets[name]
		if s.removed {
			continue // drains: no new environments
		}
		cfg := s.cfg
		if s.desired > serving[cfg.Name] {
			if s.waitingSince.IsZero() {
				s.waitingSince = now
			}
		} else {
			s.waitingSince = time.Time{}
		}
		assigned[cfg.Name] = s.desired
		demands = append(demands, scheduler.Demand{ScaleSet: cfg.Name, Desired: withWarm(s.desired, cfg), Live: serving[cfg.Name],
			MaxConcurrent: cfg.MaxConcurrent, MemoryMB: cfg.MemoryMB, WaitingSince: s.waitingSince})
	}
	c.mu.Unlock()

	if !needsMore(demands) {
		c.updateWaiting(ctx, scheduler.Plan{})
		return nil
	}
	rc, err := c.d.Runtime.Capacity(ctx)
	if err != nil {
		return fmt.Errorf("runtime capacity: %w", err)
	}
	cp := c.d.Config.Capacity
	plan := scheduler.Decide(demands, scheduler.Capacity{
		MaxEnvironments: cp.MaxEnvironments, LiveEnvironments: len(live),
		MemoryBudgetMB: cp.MemoryBudgetMB, CommittedMemoryMB: committed,
		HostAvailableMB: rc.HostMemoryAvailableMB, MemoryMarginMB: cp.MemoryMarginMB,
		ThinPoolPercent: rc.ThinPoolPercent, MaxThinPoolPercent: cp.MaxDiskPercent,
	})
	for name := range plan.Waiting {
		if assigned[name] <= serving[name] {
			delete(plan.Waiting, name) // only warm runners are missing: no job waits
		}
	}
	c.updateWaiting(ctx, plan)
	for name, n := range plan.Create {
		for range n {
			if err := c.startProvisioning(ctx, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// withWarm adds the warm runners to the assigned jobs. The warm runners fit within
// max_concurrent; only jobs beyond it make the scale set wait for its limit.
func withWarm(assigned int, cfg config.ScaleSet) int {
	if assigned > cfg.MaxConcurrent {
		return assigned
	}
	return min(assigned+cfg.WarmRunners, cfg.MaxConcurrent)
}

func isServing(state string) bool {
	for _, s := range servingStates {
		if s == state {
			return true
		}
	}
	return false
}

func needsMore(ds []scheduler.Demand) bool {
	for _, d := range ds {
		if d.Desired > d.Live {
			return true
		}
	}
	return false
}

// updateWaiting records a scaleset.waiting event when a scale set's reason changes.
func (c *Controller) updateWaiting(ctx context.Context, plan scheduler.Plan) {
	c.mu.Lock()
	type change struct{ name, reason string }
	var changes []change
	for name, s := range c.scaleSets {
		reason := string(plan.Waiting[name])
		if reason != s.waiting {
			s.waiting = reason
			changes = append(changes, change{name, reason})
		}
	}
	c.mu.Unlock()
	for _, ch := range changes {
		msg := "demand is being served"
		level := "info"
		if ch.reason != "" {
			msg = "jobs are waiting: " + ch.reason
			level = "warn"
		}
		_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "scaleset.waiting", Level: level, Message: msg,
			ScaleSet: ch.name, Data: map[string]any{"reason": ch.reason}})
	}
}

// startProvisioning inserts a pending environment synchronously (so the next
// reconcile counts it) and provisions it in the background.
func (c *Controller) startProvisioning(ctx context.Context, scaleSet string) error {
	c.mu.Lock()
	s, ok := c.scaleSets[scaleSet]
	if !ok || s.removed {
		c.mu.Unlock()
		return nil // removed after this reconcile planned it
	}
	cfg, ghID := s.cfg, s.githubID
	c.mu.Unlock()

	id := ids.NewEnvironmentID()
	token, err := ingest.NewToken()
	if err != nil {
		return err
	}
	tplRef, tplVMID := c.activeTemplate(ctx)
	e := store.Environment{ID: id, ScaleSet: scaleSet, State: string(environment.Pending), Kind: store.KindJob, TemplateVMID: tplVMID,
		RunnerName: "ghrm-" + id[len(id)-12:], TokenHash: ingest.HashToken(token), MemoryMB: cfg.MemoryMB}
	if err := c.d.Store.CreateEnvironment(ctx, e); err != nil {
		return err
	}
	_, _ = c.d.Recorder.Info(ctx, "environment.created", "environment created for "+scaleSet,
		events.Refs{ScaleSet: scaleSet, EnvironmentID: id}, map[string]any{"runner_name": e.RunnerName})
	c.log(ctx, id, "control-plane", "created for scale set %s (runner %s)", scaleSet, e.RunnerName)

	c.inflight.Add(1)
	go func() {
		defer c.inflight.Done()
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), provisionTimeout)
		defer cancel()
		c.provision(pctx, e, token, ghID, tplRef)
	}()
	return nil
}

func (c *Controller) activeTemplate(ctx context.Context) (string, int) {
	if c.d.Templates == nil {
		return "", c.d.Config.Proxmox.TemplateVMID
	}
	return c.d.Templates.Active(ctx)
}

func (c *Controller) provision(ctx context.Context, e store.Environment, token string, scaleSetID int, template string) {
	cfg := c.scaleSetConfig(e.ScaleSet)
	if _, err := c.transition(ctx, e.ID, []string{"pending"}, environment.Provisioning, nil); err != nil {
		return
	}
	c.log(ctx, e.ID, "control-plane", "requesting a just-in-time runner config")
	runnerID, jit, err := c.d.GitHub.GenerateJIT(ctx, e.ScaleSet, scaleSetID, e.RunnerName)
	if err != nil {
		c.Fail(ctx, e.ID, "jit", err)
		return
	}
	saved, err := c.d.Store.UpdateEnvironment(ctx, e.ID, func(x *store.Environment) { x.RunnerID = runnerID })
	if err != nil {
		c.Fail(ctx, e.ID, "store", err)
		return
	}
	if saved.State != string(environment.Provisioning) {
		// Destroyed (or failed) while the JIT config was being generated: do not
		// create anything, and do not leave the registration behind.
		if err := c.d.GitHub.RemoveRunner(ctx, e.ScaleSet, runnerID); err != nil {
			c.log(ctx, e.ID, "control-plane", "removing runner %d failed: %v", runnerID, err)
		}
		return
	}
	spec := runtime.EnvironmentSpec{ID: e.ID, Hostname: e.RunnerName, Cores: cfg.Cores, MemoryMB: cfg.MemoryMB, Template: template,
		Env: map[string]string{
			ingest.EnvJITConfig:   jit,
			ingest.EnvEnvironment: e.ID,
			ingest.EnvURL:         c.d.IngestURL,
			ingest.EnvToken:       token,
			ingest.EnvFingerprint: c.d.IngestFingerprint,
		}}
	c.createAndStart(ctx, e.ID, spec)
}

// createAndStart creates the runtime environment and starts it (provisioning → booting).
func (c *Controller) createAndStart(ctx context.Context, id string, spec runtime.EnvironmentSpec) {
	e := store.Environment{ID: id}
	c.log(ctx, e.ID, "runtime", "creating environment (%d cores, %d MB)", spec.Cores, spec.MemoryMB)
	start := c.now()
	ref, err := c.d.Runtime.Create(ctx, spec)
	if err != nil {
		c.log(ctx, e.ID, "runtime", "create failed: %v", err)
		c.Fail(ctx, e.ID, "create", err)
		return
	}
	c.log(ctx, e.ID, "runtime", "created %s in %s", ref, c.now().Sub(start).Round(time.Millisecond))
	if _, err := c.transition(ctx, e.ID, []string{"provisioning"}, environment.Booting, func(x *store.Environment) { x.RuntimeRef = ref.ID }); err != nil {
		return
	}
	if err := c.d.Runtime.Start(ctx, ref); err != nil {
		c.log(ctx, e.ID, "runtime", "start failed: %v", err)
		c.Fail(ctx, e.ID, "start", err)
		return
	}
	c.log(ctx, e.ID, "runtime", "started %s", ref)
}

func (c *Controller) scaleSetConfig(name string) (cfg config.ScaleSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.scaleSets[name]; ok {
		return s.cfg
	}
	return cfg
}

// Fail marks an environment failed at stage and destroys it unless its scale set
// keeps failed environments for debugging.
func (c *Controller) Fail(ctx context.Context, id, stage string, cause error) {
	reason := cause.Error()
	e, err := c.transition(ctx, id, nil, environment.Failed, func(x *store.Environment) {
		x.FailureStage, x.FailureReason = stage, reason
	})
	if err != nil {
		return
	}
	_, _ = c.d.Recorder.Error(ctx, "environment.failed", fmt.Sprintf("failed at %s: %s", stage, reason),
		events.Refs{ScaleSet: e.ScaleSet, EnvironmentID: id, JobID: e.JobID}, map[string]any{"stage": stage})
	if c.scaleSetConfig(e.ScaleSet).KeepOnFailureMinutes == 0 {
		c.destroy(ctx, id)
	}
}

// destroy tears an environment down: runtime guest, then the GitHub runner.
// Concurrent calls for the same environment run once, and failed attempts are
// retried with backoff, logging only when the error changes.
func (c *Controller) destroy(ctx context.Context, id string) {
	c.mu.Lock()
	if c.destroying[id] {
		c.mu.Unlock()
		return
	}
	if r, ok := c.retries[id]; ok && c.now().Before(r.next) {
		c.mu.Unlock()
		return
	}
	c.destroying[id] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.destroying, id)
		c.mu.Unlock()
	}()

	e, err := c.d.Store.GetEnvironment(ctx, id)
	if err != nil || e.State == string(environment.Destroyed) {
		return
	}
	if e.State != string(environment.Destroying) {
		if e, err = c.transition(ctx, id, nil, environment.Destroying, nil); err != nil {
			return
		}
	}
	if e.RuntimeRef != "" {
		if err := c.d.Runtime.Destroy(ctx, runtime.Ref{ID: e.RuntimeRef}); err != nil {
			c.destroyFailed(ctx, e, err)
			return
		}
		c.log(ctx, id, "runtime", "destroyed %s", e.RuntimeRef)
	}
	c.mu.Lock()
	delete(c.retries, id)
	c.mu.Unlock()
	if e.RunnerID != 0 {
		if err := c.d.GitHub.RemoveRunner(ctx, e.ScaleSet, e.RunnerID); err != nil {
			c.log(ctx, id, "control-plane", "removing runner %d failed: %v", e.RunnerID, err)
		}
	}
	_, _ = c.transition(ctx, id, []string{"destroying"}, environment.Destroyed, nil)
}

type retry struct {
	next    time.Time
	backoff time.Duration
	lastErr string
}

func (c *Controller) destroyFailed(ctx context.Context, e store.Environment, err error) {
	c.mu.Lock()
	r, ok := c.retries[e.ID]
	if !ok {
		r = &retry{backoff: 30 * time.Second}
		c.retries[e.ID] = r
	} else {
		r.backoff = min(r.backoff*2, 5*time.Minute)
	}
	r.next = c.now().Add(r.backoff)
	changed := r.lastErr != err.Error()
	r.lastErr = err.Error()
	c.mu.Unlock()
	if changed {
		c.log(ctx, e.ID, "runtime", "destroy failed (will retry with backoff): %v", err)
		_, _ = c.d.Recorder.Warn(ctx, "environment.destroy_failed", err.Error(), events.Refs{ScaleSet: e.ScaleSet, EnvironmentID: e.ID}, nil)
	}
}

// completingGrace is how long a completing environment may keep running before it is
// destroyed anyway. The agent flushes logs and powers off on its own, normally in seconds.
const completingGrace = 4 * time.Minute

// Teardown destroys environments whose work is over.
func (c *Controller) Teardown(ctx context.Context) {
	envs, err := c.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: []string{"completing", "failed", "destroying"}})
	if err != nil {
		return
	}
	now := c.now()
	for _, e := range envs {
		switch e.State {
		case "completing":
			running := false
			if e.RuntimeRef != "" {
				st, err := c.d.Runtime.Status(ctx, runtime.Ref{ID: e.RuntimeRef})
				running = err == nil && st.Running
				if err != nil && !errors.Is(err, runtime.ErrNotFound) {
					running = true // unknown: wait for the grace period
				}
			}
			if !running || now.Sub(e.StateChangedAt) > completingGrace {
				c.destroy(ctx, e.ID)
			}
		case "failed":
			keep := time.Duration(c.scaleSetConfig(e.ScaleSet).KeepOnFailureMinutes) * time.Minute
			if now.Sub(e.StateChangedAt) >= keep {
				c.destroy(ctx, e.ID)
			}
		case "destroying":
			c.destroy(ctx, e.ID)
		}
	}
}

// RequestDestroy destroys an environment on an operator's request.
func (c *Controller) RequestDestroy(ctx context.Context, id string) error {
	e, err := c.d.Store.GetEnvironment(ctx, id)
	if err != nil {
		return err
	}
	if e.State == string(environment.Destroyed) {
		return errors.New("environment is already destroyed")
	}
	c.destroy(context.WithoutCancel(ctx), id)
	return nil
}

// SpecialSpec describes a build or verify environment (spec §8.3, §8.4).
type SpecialSpec struct {
	Kind         string // store.KindBuild or store.KindVerify
	Template     string // runtime template reference to clone
	TemplateVMID int
	Cores        int
	MemoryMB     int
	DiskGB       int
	Env          map[string]string // mode variables; the ingest variables are added here
	// OnCreated, when set, receives the environment ID as soon as its row exists, before the
	// slow runtime work, so the caller can record it (and clean it up after a restart).
	OnCreated func(id string)
}

// StartSpecial provisions a build or verify environment and starts it. It has no scale set
// and no runner; its agent runs in the mode given by Env and reports to the ingest.
func (c *Controller) StartSpecial(ctx context.Context, s SpecialSpec) (string, error) {
	id := ids.NewEnvironmentID()
	token, err := ingest.NewToken()
	if err != nil {
		return "", err
	}
	e := store.Environment{ID: id, State: string(environment.Pending), Kind: s.Kind, TemplateVMID: s.TemplateVMID,
		RunnerName: "ghrm-" + s.Kind + "-" + id[len(id)-8:], TokenHash: ingest.HashToken(token), MemoryMB: s.MemoryMB}
	if err := c.d.Store.CreateEnvironment(ctx, e); err != nil {
		return "", err
	}
	_, _ = c.d.Recorder.Info(ctx, "environment.created", "environment created for a template "+s.Kind,
		events.Refs{EnvironmentID: id}, map[string]any{"kind": s.Kind})
	c.log(ctx, id, "control-plane", "created for a template %s", s.Kind)
	if s.OnCreated != nil {
		s.OnCreated(id)
	}
	if _, err := c.transition(ctx, id, []string{"pending"}, environment.Provisioning, nil); err != nil {
		return id, err
	}
	env := map[string]string{
		ingest.EnvEnvironment: id,
		ingest.EnvURL:         c.d.IngestURL,
		ingest.EnvToken:       token,
		ingest.EnvFingerprint: c.d.IngestFingerprint,
	}
	for k, v := range s.Env {
		env[k] = v
	}
	c.createAndStart(ctx, id, runtime.EnvironmentSpec{ID: id, Hostname: e.RunnerName, Cores: s.Cores, MemoryMB: s.MemoryMB,
		Template: s.Template, DiskGB: s.DiskGB, Env: env})
	got, err := c.d.Store.GetEnvironment(ctx, id)
	if err != nil {
		return id, err
	}
	if got.State == string(environment.Failed) {
		return id, fmt.Errorf("%s environment failed at %s: %s", s.Kind, got.FailureStage, got.FailureReason)
	}
	return id, nil
}
