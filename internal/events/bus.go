// Package events fans persisted events out to in-process subscribers (spec §7).
package events

import (
	"sync"
	"sync/atomic"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Bus delivers events to subscribers without ever blocking the publisher.
type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscription]struct{}
}

// Subscription receives published events on C until Close.
type Subscription struct {
	C      <-chan store.Event
	ch     chan store.Event
	bus    *Bus
	lagged atomic.Bool
	once   sync.Once
}

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{subs: map[*Subscription]struct{}{}} }

// Subscribe registers a subscriber with the given channel buffer.
func (b *Bus) Subscribe(buffer int) *Subscription {
	ch := make(chan store.Event, buffer)
	s := &Subscription{C: ch, ch: ch, bus: b}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Publish delivers e to every subscriber with room in its buffer. A subscriber
// whose buffer is full is marked lagged and misses e; it must resync from the store.
func (b *Bus) Publish(e store.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			s.lagged.Store(true)
		}
	}
}

// Lagged reports whether the subscriber missed events.
func (s *Subscription) Lagged() bool { return s.lagged.Load() }

// ResetLagged clears the lagged flag after the consumer resynced.
func (s *Subscription) ResetLagged() { s.lagged.Store(false) }

// Close unregisters the subscriber and closes C.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s)
		s.bus.mu.Unlock()
		close(s.ch)
	})
}
