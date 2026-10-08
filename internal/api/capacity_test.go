package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestCapacityIsSharedBrieflyAndSurvivesABlip(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	calls := 0
	var fail error
	cached := cachedCapacity(func(context.Context) (runtime.Capacity, error) {
		calls++
		if fail != nil {
			return runtime.Capacity{}, fail
		}
		return runtime.Capacity{Environments: calls}, nil
	}, clk.now, 5*time.Second, time.Minute)
	ctx := context.Background()

	if c, err := cached(ctx); err != nil || c.Environments != 1 {
		t.Fatalf("first = %+v, %v", c, err)
	}
	clk.add(2 * time.Second)
	if c, _ := cached(ctx); c.Environments != 1 || calls != 1 {
		t.Fatalf("within the TTL: %+v after %d calls, want the shared answer", c, calls)
	}

	// One failed answer keeps the last good one: a blip is not an outage.
	clk.add(10 * time.Second)
	fail = errors.New("proxmox GET /nodes/pve/lxc: 500")
	if c, err := cached(ctx); err != nil || c.Environments != 1 {
		t.Fatalf("after a blip = %+v, %v, want the last good answer", c, err)
	}
	// Failing for longer than the grace is an outage; the failure is shared for the TTL too.
	clk.add(2 * time.Minute)
	if _, err := cached(ctx); err == nil {
		t.Fatal("after the grace, the error must show")
	}
	before := calls
	if _, err := cached(ctx); err == nil || calls != before {
		t.Fatalf("within the TTL of a failure: err %v after %d new calls, want the shared error", err, calls-before)
	}
	fail = nil
	clk.add(10 * time.Second)
	before = calls
	if _, err := cached(ctx); err != nil || calls != before+1 {
		t.Fatalf("after recovery: err %v, %d new calls, want one fresh answer", err, calls-before)
	}
}

func TestCapacityFirstFailureShows(t *testing.T) {
	cached := cachedCapacity(func(context.Context) (runtime.Capacity, error) {
		return runtime.Capacity{}, errors.New("down")
	}, time.Now, 5*time.Second, time.Minute)
	if _, err := cached(context.Background()); err == nil {
		t.Fatal("with no good answer yet, the error must show")
	}
}

func TestCapacityConcurrentCallersShareOneCallAndMayLeave(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	cached := cachedCapacity(func(context.Context) (runtime.Capacity, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return runtime.Capacity{Environments: 3}, nil
	}, time.Now, 5*time.Second, time.Minute)

	// A caller that gives up does not wait for the slow runtime.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := cached(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a canceled caller = %v, want its context's error", err)
	}

	var wg sync.WaitGroup
	results := make([]int, 5)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := cached(context.Background())
			results[i] = c.Environments
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	for i, r := range results {
		if r != 3 {
			t.Fatalf("caller %d got %d, want 3", i, r)
		}
	}
	if calls != 1 {
		t.Fatalf("runtime asked %d times, want 1 shared call", calls)
	}
}
