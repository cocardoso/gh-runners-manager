package main

import (
	"context"
	"reflect"
	"sync"

	"github.com/cocardoso/gh-runners-manager/internal/config"
)

// supervisor keeps one listener per scale set in line with the settings: it starts new
// ones, restarts changed ones (after the old one has stopped, so GitHub never sees two
// sessions) and stops removed ones.
type supervisor struct {
	start func(ctx context.Context, ss config.ScaleSet)

	mu      sync.Mutex
	running map[string]*listenerRun
	wg      sync.WaitGroup
}

type listenerRun struct {
	cfg    config.ScaleSet
	cancel context.CancelFunc
	done   chan struct{}
}

func newSupervisor(start func(ctx context.Context, ss config.ScaleSet)) *supervisor {
	return &supervisor{start: start, running: map[string]*listenerRun{}}
}

// Reconcile applies the current list; listeners derive from ctx.
func (s *supervisor) Reconcile(ctx context.Context, list []config.ScaleSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]config.ScaleSet{}
	for _, ss := range list {
		want[ss.Name] = ss
	}
	for name, r := range s.running {
		if ss, ok := want[name]; !ok || !reflect.DeepEqual(session(ss), session(r.cfg)) {
			r.cancel()
			<-r.done
			delete(s.running, name)
		}
	}
	for _, ss := range list {
		if _, ok := s.running[ss.Name]; ok {
			continue
		}
		lctx, cancel := context.WithCancel(ctx)
		r := &listenerRun{cfg: ss, cancel: cancel, done: make(chan struct{})}
		s.running[ss.Name] = r
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer close(r.done)
			s.start(lctx, ss)
		}()
	}
}

// session keeps what a listener's GitHub session is made of. The runner settings (sizes,
// warm runners, template profile, keep time) are read from the controller, so changing
// them must not restart the listener: the card would show it stopped meanwhile.
func session(ss config.ScaleSet) config.ScaleSet {
	return config.ScaleSet{Name: ss.Name, URL: ss.URL, Credential: ss.Credential, RunnerGroup: ss.RunnerGroup,
		Labels: ss.Labels, MaxConcurrent: ss.MaxConcurrent}
}

// Wait blocks until every listener has stopped (after ctx ends).
func (s *supervisor) Wait() { s.wg.Wait() }
