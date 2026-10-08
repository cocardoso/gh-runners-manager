// Package controller turns scale set demand into job environments and drives
// their lifecycle (spec §4.1, §5, §9).
package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/actions/scaleset/listener"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// GitHub is what the controller needs from GitHub's scale set API.
type GitHub interface {
	EnsureScaleSet(ctx context.Context, cfg config.ScaleSet) (int, error)
	GenerateJIT(ctx context.Context, scaleSet string, scaleSetID int, runnerName string) (runnerID int64, encodedJIT string, err error)
	RemoveRunner(ctx context.Context, scaleSet string, runnerID int64) error
	Listen(ctx context.Context, scaleSet string, scaleSetID, maxRunners int, scaler listener.Scaler) error
}

// Deps are the controller's collaborators.
type Deps struct {
	Store             *store.Store
	Recorder          *events.Recorder
	Runtime           runtime.Runtime
	GitHub            GitHub
	Logs              *logs.Store
	Config            *config.Config
	IngestURL         string
	IngestFingerprint string
	// FirewallProbe is the address agents probe to know the job network's firewall applies
	// to them ("" when the control plane does not serve it: the runtime waits instead).
	FirewallProbe string
	Now           func() time.Time
	Timeouts      environment.Timeouts
	// Templates chooses the template job environments clone (nil: the configured bootstrap template).
	Templates TemplateSource
	// TemplateEvents receives agent events of build and verify environments.
	TemplateEvents TemplateEvents
	// Stages, when set, receives how long each environment stayed in a state.
	Stages StageObserver
}

// StageObserver records how long environments stay in each state (metrics).
type StageObserver interface {
	ObserveStage(stage string, d time.Duration)
}

// TemplateSource reports the active template: the runtime reference environments clone
// ("" for the configured one) and its VMID (recorded for retention).
type TemplateSource interface {
	Active(ctx context.Context) (ref string, vmid int)
}

// FirewallGatedSource is a TemplateSource that also tells whether the active template's agent waits for the
// job network's firewall before it starts the runner.
type FirewallGatedSource interface {
	ActiveFirewallGated(ctx context.Context) (ref string, vmid int, gated bool)
}

// TemplateEvents receives agent events of build and verify environments.
type TemplateEvents interface {
	AgentEvent(ctx context.Context, envID, name string, at time.Time, data map[string]any)
}

// ScaleSetStatus is the controller's live view of one scale set.
type ScaleSetStatus struct {
	Name         string
	GitHubID     int
	Desired      int
	Live         int
	Waiting      string
	WaitingSince time.Time
	Listening    bool
	ListenError  string
	// Removed means the scale set was deleted from the settings; it stays listed
	// until its live environments are gone and gets no new ones.
	Removed bool
}

type scaleSetState struct {
	cfg          config.ScaleSet
	githubID     int
	desired      int
	waitingSince time.Time
	waiting      string
	listening    bool
	listenErr    string
	removed      bool
}

// Controller provisions and tears down environments.
type Controller struct {
	d Deps

	mu         sync.Mutex
	scaleSets  map[string]*scaleSetState
	order      []string          // scale set names in settings order, removed ones last
	destroying map[string]bool   // single-flight destroys
	retries    map[string]*retry // destroy backoff

	reconcileMu sync.Mutex
	inflight    sync.WaitGroup
	kick        chan struct{}
}

// New returns a Controller for the scale sets in d.Config.
func New(d Deps) *Controller {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Timeouts == nil {
		d.Timeouts = environment.DefaultTimeouts()
	}
	c := &Controller{d: d, scaleSets: map[string]*scaleSetState{}, destroying: map[string]bool{},
		retries: map[string]*retry{}, kick: make(chan struct{}, 1)}
	c.UpdateScaleSets(d.Config.ScaleSets)
	return c
}

func (c *Controller) now() time.Time { return c.d.Now() }

// SetTemplates connects the template service, which itself needs the controller.
// Call it before Run.
func (c *Controller) SetTemplates(src TemplateSource, ev TemplateEvents) {
	c.d.Templates, c.d.TemplateEvents = src, ev
}

// SetStages connects the stage-duration observer (metrics). Call it before Run.
func (c *Controller) SetStages(o StageObserver) { c.d.Stages = o }

// SetScaleSetID records the GitHub ID of a scale set.
func (c *Controller) SetScaleSetID(name string, id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.scaleSets[name]; ok {
		s.githubID = id
	}
}

// SetListening records the listener state of a scale set.
func (c *Controller) SetListening(name string, listening bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.scaleSets[name]; ok {
		s.listening = listening
		s.listenErr = ""
		if err != nil {
			s.listenErr = err.Error()
		}
	}
}

// ScaleSets returns the live status of every scale set.
func (c *Controller) ScaleSets(ctx context.Context) []ScaleSetStatus {
	live := map[string]int{}
	if envs, err := c.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: servingStates}); err == nil {
		for _, e := range envs {
			live[e.ScaleSet]++
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ScaleSetStatus, 0, len(c.order))
	for _, name := range c.order {
		s := c.scaleSets[name]
		if s.removed && live[name] == 0 {
			continue // drained; Draining forgets it
		}
		out = append(out, ScaleSetStatus{Name: name, GitHubID: s.githubID, Desired: s.desired, Live: live[name],
			Waiting: s.waiting, WaitingSince: s.waitingSince, Listening: s.listening, ListenError: s.listenErr, Removed: s.removed})
	}
	return out
}

// Kick asks the run loop to reconcile soon.
func (c *Controller) Kick() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Wait blocks until in-flight provisioning finishes.
func (c *Controller) Wait() { c.inflight.Wait() }

// Run reconciles, tears down and reaps environments until ctx ends.
func (c *Controller) Run(ctx context.Context) {
	c.Reap(ctx)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	reap := time.NewTicker(30 * time.Second)
	defer reap.Stop()
	for {
		if err := c.Reconcile(ctx); err != nil && ctx.Err() == nil {
			_, _ = c.d.Recorder.Error(ctx, "controller.error", "reconcile failed: "+err.Error(), events.Refs{}, nil)
		}
		c.Teardown(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kick:
		case <-reap.C:
			c.Reap(ctx)
		}
	}
}

// Resolve implements ingest.TokenResolver.
func (c *Controller) Resolve(ctx context.Context, tokenHash string) (string, bool) {
	e, err := c.d.Store.FindEnvironmentByToken(ctx, tokenHash)
	if err != nil {
		return "", false
	}
	switch environment.State(e.State) {
	case environment.Destroyed, environment.Destroying:
		return "", false
	}
	return e.ID, true
}

func (c *Controller) log(ctx context.Context, envID, stream, format string, a ...any) {
	_ = c.d.Logs.Write(ctx, envID, stream, fmt.Sprintf(format, a...), c.now())
}

// transition moves an environment and records it as an event and a control-plane log line.
func (c *Controller) transition(ctx context.Context, id string, from []string, to environment.State, mutate func(*store.Environment)) (store.Environment, error) {
	before := ""
	var since time.Time
	e, err := c.d.Store.TransitionEnvironment(ctx, id, from, string(to), func(e *store.Environment) {
		before, since = e.State, e.StateChangedAt
		if mutate != nil {
			mutate(e)
		}
	})
	if err != nil {
		return e, err
	}
	if c.d.Stages != nil && !since.IsZero() && before != string(to) {
		c.d.Stages.ObserveStage(before, c.now().Sub(since))
	}
	level := "info"
	if to == environment.Failed {
		level = "error"
	}
	_, _ = c.d.Recorder.Record(ctx, store.Event{Kind: "environment.state", Level: level,
		Message:  fmt.Sprintf("%s → %s", before, to),
		ScaleSet: e.ScaleSet, EnvironmentID: e.ID, JobID: e.JobID,
		Data: map[string]any{"from": before, "to": string(to)}})
	c.log(ctx, e.ID, "control-plane", "state %s -> %s", before, to)
	return e, nil
}

// advance walks an environment along booting → connected → idle → running → completing
// until it reaches target, taking direct transitions when the state machine allows them.
func (c *Controller) advance(ctx context.Context, id string, target environment.State, mutate func(*store.Environment)) (store.Environment, bool) {
	path := []environment.State{environment.Booting, environment.Connected, environment.Idle, environment.Running, environment.Completing}
	for range len(path) {
		e, err := c.d.Store.GetEnvironment(ctx, id)
		if err != nil {
			return e, false
		}
		cur := environment.State(e.State)
		if cur == target {
			return e, true
		}
		next := target
		if !environment.CanTransition(cur, target) {
			next = ""
			for i, s := range path {
				if s == cur && i+1 < len(path) && pathIndex(path, target) > i {
					next = path[i+1]
				}
			}
			if next == "" {
				return e, false // target is behind or unreachable: ignore the late event
			}
		}
		var m func(*store.Environment)
		if next == target {
			m = mutate
		}
		if _, err := c.transition(ctx, id, []string{string(cur)}, next, m); err != nil && !errors.Is(err, store.ErrConflict) {
			return e, false
		}
	}
	e, err := c.d.Store.GetEnvironment(ctx, id)
	return e, err == nil && environment.State(e.State) == target
}

func pathIndex(path []environment.State, s environment.State) int {
	for i, p := range path {
		if p == s {
			return i
		}
	}
	return -1
}
