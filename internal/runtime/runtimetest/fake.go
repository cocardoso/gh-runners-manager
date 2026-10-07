// Package runtimetest provides an in-memory runtime.Runtime for tests.
package runtimetest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

// Fake is an in-memory runtime. It is safe for concurrent use.
type Fake struct {
	mu   sync.Mutex
	next int
	envs map[string]*fakeEnv // keyed by ref ID

	// CreateErr, when non-nil, is returned by Create.
	CreateErr error
	// Cap is returned by Capacity, with Environments filled in.
	Cap runtime.Capacity
	// StaleList makes List report every environment as not running, like a
	// cached hypervisor listing right after a start.
	StaleList bool
	// ListExclude hides environments (by environment ID) from List, like a snapshot
	// taken before they were created.
	ListExclude map[string]bool
	// DestroyErr, when non-nil, is returned by Destroy (the environment is kept).
	DestroyErr error
	// DestroyDelay makes Destroy slow, to exercise concurrent callers.
	DestroyDelay time.Duration
	// DestroyCalls counts Destroy calls for existing environments.
	DestroyCalls int
}

type fakeEnv struct {
	spec    runtime.EnvironmentSpec
	running bool
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{envs: map[string]*fakeEnv{}} }

func (f *Fake) Create(_ context.Context, spec runtime.EnvironmentSpec) (runtime.Ref, error) {
	if err := spec.Validate(); err != nil {
		return runtime.Ref{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateErr != nil {
		return runtime.Ref{}, f.CreateErr
	}
	for id, e := range f.envs {
		if e.spec.ID == spec.ID {
			return runtime.Ref{ID: id}, nil
		}
	}
	f.next++
	ref := runtime.Ref{ID: fmt.Sprintf("fake-%d", f.next)}
	f.envs[ref.ID] = &fakeEnv{spec: spec}
	return ref, nil
}

func (f *Fake) Start(_ context.Context, ref runtime.Ref) error { return f.setRunning(ref, true) }
func (f *Fake) Stop(_ context.Context, ref runtime.Ref) error  { return f.setRunning(ref, false) }

func (f *Fake) setRunning(ref runtime.Ref, running bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[ref.ID]
	if !ok {
		return runtime.ErrNotFound
	}
	e.running = running
	return nil
}

func (f *Fake) Destroy(_ context.Context, ref runtime.Ref) error {
	f.mu.Lock()
	delay := f.DestroyDelay
	if _, ok := f.envs[ref.ID]; ok {
		f.DestroyCalls++
	}
	f.mu.Unlock()
	time.Sleep(delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DestroyErr != nil {
		return f.DestroyErr
	}
	delete(f.envs, ref.ID)
	return nil
}

func (f *Fake) List(_ context.Context) ([]runtime.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.Status, 0, len(f.envs))
	for id, e := range f.envs {
		if f.ListExclude[e.spec.ID] {
			continue
		}
		st := f.status(id, e)
		if f.StaleList {
			st.Running, st.IP = false, ""
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.ID < out[j].Ref.ID })
	return out, nil
}

func (f *Fake) Status(_ context.Context, ref runtime.Ref) (runtime.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[ref.ID]
	if !ok {
		return runtime.Status{}, runtime.ErrNotFound
	}
	return f.status(ref.ID, e), nil
}

func (f *Fake) status(id string, e *fakeEnv) runtime.Status {
	st := runtime.Status{Ref: runtime.Ref{ID: id}, EnvironmentID: e.spec.ID, Running: e.running}
	if e.running {
		st.IP = "192.0.2.10"
	}
	return st
}

func (f *Fake) Capacity(_ context.Context) (runtime.Capacity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.Cap
	c.Environments = len(f.envs)
	return c, nil
}

// Spec returns the spec an environment was created with.
func (f *Fake) Spec(ref runtime.Ref) (runtime.EnvironmentSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[ref.ID]
	if !ok {
		return runtime.EnvironmentSpec{}, false
	}
	return e.spec, true
}
