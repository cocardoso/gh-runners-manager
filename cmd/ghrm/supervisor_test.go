package main

import (
	"context"
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
