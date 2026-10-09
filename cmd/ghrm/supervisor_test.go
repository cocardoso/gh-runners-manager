package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
)

type listenerLog struct {
	mu      sync.Mutex
	started map[string]int
	running map[string]int
}

func (l *listenerLog) start(ctx context.Context, ss config.ScaleSet) {
	l.mu.Lock()
	l.started[ss.Name]++
	l.running[ss.Name]++
	if l.running[ss.Name] > 1 {
		panic("two listeners for " + ss.Name)
	}
	l.mu.Unlock()
	<-ctx.Done()
	time.Sleep(5 * time.Millisecond) // closing the session takes a moment
	l.mu.Lock()
	l.running[ss.Name]--
	l.mu.Unlock()
}

func (l *listenerLog) counts() (map[string]int, map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, r := map[string]int{}, map[string]int{}
	for k, v := range l.started {
		s[k] = v
	}
	for k, v := range l.running {
		r[k] = v
	}
	return s, r
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSupervisorStartsRestartsAndStopsListeners(t *testing.T) {
	l := &listenerLog{started: map[string]int{}, running: map[string]int{}}
	sup := newSupervisor(l.start)
	ctx, cancel := context.WithCancel(context.Background())
	a := config.ScaleSet{Name: "a", URL: "https://github.com/o/a", MemoryMB: 512}
	b := config.ScaleSet{Name: "b", URL: "https://github.com/o/b", MemoryMB: 512}
	sup.Reconcile(ctx, []config.ScaleSet{a, b})
	waitFor(t, func() bool { _, r := l.counts(); return r["a"] == 1 && r["b"] == 1 })

	sup.Reconcile(ctx, []config.ScaleSet{a, b}) // unchanged: nothing restarts
	a2 := a
	a2.Labels = []string{"x"}
	sup.Reconcile(ctx, []config.ScaleSet{a2, b})
	waitFor(t, func() bool { s, r := l.counts(); return s["a"] == 2 && r["a"] == 1 })

	sup.Reconcile(ctx, []config.ScaleSet{b})
	waitFor(t, func() bool { _, r := l.counts(); return r["a"] == 0 })
	if s, _ := l.counts(); s["b"] != 1 {
		t.Fatalf("b restarted %d times, want once", s["b"])
	}
	cancel()
	sup.Wait()
	if _, r := l.counts(); r["b"] != 0 {
		t.Fatal("listeners must stop with the context")
	}
}

func TestSupervisorKeepsTheListenerWhenOnlyRunnerSettingsChange(t *testing.T) {
	l := &listenerLog{started: map[string]int{}, running: map[string]int{}}
	sup := newSupervisor(l.start)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); sup.Wait() }()
	a := config.ScaleSet{Name: "a", URL: "https://github.com/o/a", Credential: "c", MaxConcurrent: 2, Cores: 2, MemoryMB: 4096}
	sup.Reconcile(ctx, []config.ScaleSet{a})
	waitFor(t, func() bool { _, r := l.counts(); return r["a"] == 1 })

	// What GitHub does not see (sizes, warm runners, profile, keep time) leaves the session alone.
	a2 := a
	a2.Cores, a2.MemoryMB, a2.WarmRunners, a2.TemplateProfile, a2.KeepOnFailureMinutes = 4, 8192, 1, "slim", 15
	sup.Reconcile(ctx, []config.ScaleSet{a2})
	if s, _ := l.counts(); s["a"] != 1 {
		t.Fatalf("listener restarted %d times for a runner-only change", s["a"]-1)
	}
	// The session's own settings still restart it.
	a3 := a2
	a3.MaxConcurrent = 3
	sup.Reconcile(ctx, []config.ScaleSet{a3})
	waitFor(t, func() bool { s, r := l.counts(); return s["a"] == 2 && r["a"] == 1 })
}

func TestSupervisorSaysWhyAListenerStopped(t *testing.T) {
	causes := make(chan error, 4)
	sup := newSupervisor(func(ctx context.Context, _ config.ScaleSet) {
		<-ctx.Done()
		causes <- context.Cause(ctx)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); sup.Wait() }()
	a := config.ScaleSet{Name: "a", URL: "https://github.com/o/a"}
	sup.Reconcile(ctx, []config.ScaleSet{a})
	a2 := a
	a2.Labels = []string{"x"}
	sup.Reconcile(ctx, []config.ScaleSet{a2})
	if err := <-causes; !errors.Is(err, errListenerRestart) {
		t.Fatalf("a restarted listener's cause = %v, want errListenerRestart", err)
	}
	sup.Reconcile(ctx, nil)
	if err := <-causes; errors.Is(err, errListenerRestart) {
		t.Fatal("a removed scale set's listener must not look restarted")
	}
	// No labels and an empty list are the same session.
	b := config.ScaleSet{Name: "b", URL: "https://github.com/o/b"}
	sup.Reconcile(ctx, []config.ScaleSet{b})
	b.Labels = []string{}
	sup.Reconcile(ctx, []config.ScaleSet{b})
	select {
	case err := <-causes:
		t.Fatalf("listener b restarted (%v)", err)
	case <-time.After(20 * time.Millisecond):
	}
}
