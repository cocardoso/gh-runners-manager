package api

import (
	"context"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

// cachedCapacity shares one capacity answer for ttl, as the overview is fetched again on
// every event, and keeps the last good answer for grace after a failure: one refused call
// to the runtime is a blip, not an outage worth a red alert.
func cachedCapacity(fn func(context.Context) (runtime.Capacity, error), now func() time.Time, ttl, grace time.Duration) func(context.Context) (runtime.Capacity, error) {
	var (
		mu      sync.Mutex
		last    runtime.Capacity
		okAt    time.Time // when last was fetched
		askedAt time.Time // when the runtime was last asked
	)
	return func(ctx context.Context) (runtime.Capacity, error) {
		mu.Lock()
		defer mu.Unlock()
		t := now()
		if !askedAt.IsZero() && t.Sub(askedAt) < ttl && !okAt.IsZero() && t.Sub(okAt) < grace {
			return last, nil
		}
		askedAt = t
		c, err := fn(ctx)
		if err == nil {
			last, okAt = c, t
			return c, nil
		}
		if !okAt.IsZero() && t.Sub(okAt) < grace {
			return last, nil
		}
		return runtime.Capacity{}, err
	}
}
