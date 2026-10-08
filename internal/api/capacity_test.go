package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

func TestCapacityIsSharedBrieflyAndSurvivesABlip(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	calls := 0
	var fail error
	cached := cachedCapacity(func(context.Context) (runtime.Capacity, error) {
		calls++
		if fail != nil {
			return runtime.Capacity{}, fail
		}
		return runtime.Capacity{Environments: calls}, nil
	}, func() time.Time { return now }, 5*time.Second, time.Minute)
	ctx := context.Background()

	if c, err := cached(ctx); err != nil || c.Environments != 1 {
		t.Fatalf("first = %+v, %v", c, err)
	}
	now = now.Add(2 * time.Second)
	if c, _ := cached(ctx); c.Environments != 1 || calls != 1 {
		t.Fatalf("within the TTL: %+v after %d calls, want the shared answer", c, calls)
	}

	// One failed answer keeps the last good one: a blip is not an outage.
	now = now.Add(10 * time.Second)
	fail = errors.New("proxmox GET /nodes/pve/lxc: 500")
	if c, err := cached(ctx); err != nil || c.Environments != 1 {
		t.Fatalf("after a blip = %+v, %v, want the last good answer", c, err)
	}
	// Failing for longer than the grace is an outage.
	now = now.Add(2 * time.Minute)
	if _, err := cached(ctx); err == nil {
		t.Fatal("after the grace, the error must show")
	}
	fail = nil
	now = now.Add(10 * time.Second)
	if c, err := cached(ctx); err != nil || c.Environments == 1 {
		t.Fatalf("after recovery = %+v, %v, want a fresh answer", c, err)
	}
}
