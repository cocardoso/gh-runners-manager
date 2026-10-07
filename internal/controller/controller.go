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
	Now               func() time.Time
	Timeouts          environment.Timeouts
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
}

type scaleSetState struct {
	cfg          config.ScaleSet
	githubID     int
	desired      int
	waitingSince time.Time
	waiting      string
	listening    bool
	listenErr    string
}

// Controller provisions and tears down environments.
type Controller struct {
	d Deps

	mu        sync.Mutex
	scaleSets map[string]*scaleSetState

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
	c := &Controller{d: d, scaleSets: map[string]*scaleSetState{}, kick: make(chan struct{}, 1)}
	for _, ss := range d.Config.ScaleSets {
		c.scaleSets[ss.Name] = &scaleSetState{cfg: ss}
	}
	return c
}

func (c *Controller) now() time.Time { return c.d.Now() }

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
	out := make([]ScaleSetStatus, 0, len(c.d.Config.ScaleSets))
	for _, cfg := range c.d.Config.ScaleSets {
		s := c.scaleSets[cfg.Name]
		out = append(out, ScaleSetStatus{Name: cfg.Name, GitHubID: s.githubID, Desired: s.desired, Live: live[cfg.Name],
			Waiting: s.waiting, WaitingSince: s.waitingSince, Listening: s.listening, ListenError: s.listenErr})
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

// Run reconciles and tears down environments until ctx ends.
func (c *Controller) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
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
	e, err := c.d.Store.TransitionEnvironment(ctx, id, from, string(to), func(e *store.Environment) {
		before = e.State
		if mutate != nil {
			mutate(e)
		}
	})
	if err != nil {
		return e, err
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
