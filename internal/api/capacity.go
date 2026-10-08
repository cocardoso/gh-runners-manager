package api

import (
	"context"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

// capacityCallTimeout bounds one shared call to the runtime.
const capacityCallTimeout = 30 * time.Second

// cachedCapacity shares one capacity answer for ttl, as the overview is fetched again on
// every event, and keeps the last good answer for grace after a failure: one refused call
// to the runtime is a blip, not an outage worth a red alert. Concurrent callers share one
// call, made without holding the lock, and each may leave when its own context ends.
func cachedCapacity(fn func(context.Context) (runtime.Capacity, error), now func() time.Time, ttl, grace time.Duration) func(context.Context) (runtime.Capacity, error) {
	type call struct {
		done chan struct{}
		c    runtime.Capacity
		err  error
	}
	var (
		mu       sync.Mutex
		last     runtime.Capacity
		okAt     time.Time // when last was fetched
		lastErr  error     // the latest answer's error
		askedAt  time.Time // when the latest answer came
		inflight *call
	)
	// answer applies the grace to the latest answer; mu must be held.
	answer := func(t time.Time) (runtime.Capacity, error) {
		if lastErr == nil || (!okAt.IsZero() && t.Sub(okAt) < grace) {
			return last, nil
		}
		return runtime.Capacity{}, lastErr
	}
	return func(ctx context.Context) (runtime.Capacity, error) {
		mu.Lock()
		t := now()
		if !askedAt.IsZero() && t.Sub(askedAt) < ttl {
			defer mu.Unlock()
			return answer(t)
		}
		cl := inflight
		if cl == nil {
			cl = &call{done: make(chan struct{})}
			inflight = cl
			go func() {
				cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), capacityCallTimeout)
				c, err := fn(cctx)
				cancel()
				mu.Lock()
				done := now()
				askedAt, lastErr = done, err
				if err == nil {
					last, okAt = c, done
				}
				inflight = nil
				mu.Unlock()
				close(cl.done)
			}()
		}
		mu.Unlock()
		select {
		case <-ctx.Done():
			return runtime.Capacity{}, ctx.Err()
		case <-cl.done:
		}
		mu.Lock()
		defer mu.Unlock()
		return answer(now())
	}
}
