package events

import (
	"context"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Refs links an event to the objects it is about.
type Refs struct {
	ScaleSet      string
	EnvironmentID string
	JobID         string
}

// Recorder persists events, then publishes them.
type Recorder struct {
	store *store.Store
	bus   *Bus
	now   func() time.Time
}

// NewRecorder returns a Recorder. now may be nil (time.Now).
func NewRecorder(s *store.Store, b *Bus, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	return &Recorder{store: s, bus: b, now: now}
}

// Bus returns the bus events are published on.
func (r *Recorder) Bus() *Bus { return r.bus }

// Record persists e and then publishes it with its sequence number.
func (r *Recorder) Record(ctx context.Context, e store.Event) (store.Event, error) {
	if e.Time.IsZero() {
		e.Time = r.now()
	}
	saved, err := r.store.AppendEvent(ctx, e)
	if err != nil {
		return store.Event{}, err
	}
	r.bus.Publish(saved)
	return saved, nil
}

func (r *Recorder) record(ctx context.Context, level, kind, msg string, refs Refs, data map[string]any) (store.Event, error) {
	return r.Record(ctx, store.Event{Kind: kind, Level: level, Message: msg, ScaleSet: refs.ScaleSet,
		EnvironmentID: refs.EnvironmentID, JobID: refs.JobID, Data: data})
}

// Info records an informational event.
func (r *Recorder) Info(ctx context.Context, kind, msg string, refs Refs, data map[string]any) (store.Event, error) {
	return r.record(ctx, "info", kind, msg, refs, data)
}

// Warn records a warning event.
func (r *Recorder) Warn(ctx context.Context, kind, msg string, refs Refs, data map[string]any) (store.Event, error) {
	return r.record(ctx, "warn", kind, msg, refs, data)
}

// Error records an error event.
func (r *Recorder) Error(ctx context.Context, kind, msg string, refs Refs, data map[string]any) (store.Event, error) {
	return r.record(ctx, "error", kind, msg, refs, data)
}
