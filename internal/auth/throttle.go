package auth

import (
	"sync"
	"time"
)

const (
	maxFailures  = 5
	failureSpan  = 15 * time.Minute
	firstLockout = time.Minute
	maxLockout   = time.Hour
)

// throttle limits sign-in attempts per remote address: after maxFailures failures within
// failureSpan the address is locked out, for twice as long after each further lockout.
type throttle struct {
	mu    sync.Mutex
	addrs map[string]*attempts
}

type attempts struct {
	failures    []time.Time
	lockedUntil time.Time
	lockout     time.Duration
}

func (t *throttle) locked(addr string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.addrs[addr]
	return a != nil && now.Before(a.lockedUntil)
}

func (t *throttle) fail(addr string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.addrs == nil {
		t.addrs = map[string]*attempts{}
	}
	a := t.addrs[addr]
	if a == nil {
		a = &attempts{}
		t.addrs[addr] = a
	}
	recent := a.failures[:0]
	for _, f := range a.failures {
		if now.Sub(f) < failureSpan {
			recent = append(recent, f)
		}
	}
	a.failures = append(recent, now)
	if len(a.failures) >= maxFailures {
		if a.lockout == 0 {
			a.lockout = firstLockout
		} else {
			a.lockout = min(a.lockout*2, maxLockout)
		}
		a.lockedUntil = now.Add(a.lockout)
		a.failures = nil
	}
	// Forget addresses that have been quiet for a while, so the map stays small.
	for k, v := range t.addrs {
		if len(v.failures) == 0 && now.Sub(v.lockedUntil) > maxLockout {
			delete(t.addrs, k)
		}
	}
}

func (t *throttle) succeed(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.addrs, addr)
}
