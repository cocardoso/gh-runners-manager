// Package retention deletes old history: finished environments with their jobs and
// logs, events, and failed or deleted template records, by hand or once a day.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

const (
	ModeAutomatic = "automatic"
	ModeManual    = "manual"
)

// Settings say whether history is cleaned every day and how long it is kept.
type Settings struct {
	Mode      string `json:"mode" enum:"automatic,manual"`
	Days      int    `json:"days" minimum:"1" maximum:"365"`
	AuditDays int    `json:"audit_days" minimum:"1" maximum:"3650"`
}

// Defaults apply until the settings are saved.
var Defaults = Settings{Mode: ModeAutomatic, Days: 30, AuditDays: 365}

// ErrInvalid is returned for settings out of range.
var ErrInvalid = errors.New("retention: invalid settings")

const day = 24 * time.Hour

// Retention cleans history.
type Retention struct {
	Store    *store.Store
	Logs     *logs.Store
	Recorder *events.Recorder
	// Hour is the local hour of the daily sweep (it runs at half past).
	Hour int
	Now  func() time.Time
	// VacuumMin is how many deleted rows trigger compacting the database (0: 1000).
	VacuumMin int

	vacuum func(context.Context) error // tests replace the store's VACUUM
}

func (r *Retention) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// metaKey holds the settings as one JSON record, so a change is written at once.
const metaKey = "history.settings"

// Settings returns the saved settings; a missing or out-of-range value takes its default.
func (r *Retention) Settings(ctx context.Context) (Settings, error) {
	v, err := r.Store.GetMeta(ctx, metaKey)
	if errors.Is(err, store.ErrNotFound) {
		return Defaults, nil
	} else if err != nil {
		return Defaults, err
	}
	var s Settings
	if json.Unmarshal([]byte(v), &s) != nil {
		return Defaults, nil
	}
	if s.Mode != ModeAutomatic && s.Mode != ModeManual {
		s.Mode = Defaults.Mode
	}
	if s.Days < 1 || s.Days > 365 {
		s.Days = Defaults.Days
	}
	if s.AuditDays < s.Days || s.AuditDays > 3650 {
		s.AuditDays = max(Defaults.AuditDays, s.Days)
	}
	return s, nil
}

func (s Settings) validate() error {
	switch {
	case s.Mode != ModeAutomatic && s.Mode != ModeManual:
		return fmt.Errorf("%w: mode must be automatic or manual", ErrInvalid)
	case s.Days < 1 || s.Days > 365:
		return fmt.Errorf("%w: keep history 1 to 365 days", ErrInvalid)
	case s.AuditDays < s.Days || s.AuditDays > 3650:
		return fmt.Errorf("%w: keep audit events at least as long as history, up to 3650 days", ErrInvalid)
	}
	return nil
}

func (r *Retention) PutSettings(ctx context.Context, s Settings) error {
	if err := s.validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.Store.PutMeta(ctx, metaKey, string(b))
}

// Cutoffs are the configured history and audit cut-offs from now.
func (r *Retention) Cutoffs(ctx context.Context) (time.Time, time.Time, error) {
	s, err := r.Settings(ctx)
	now := r.now()
	return now.Add(-time.Duration(s.Days) * day), now.Add(-time.Duration(s.AuditDays) * day), err
}

func clamp(before, auditBefore time.Time) time.Time {
	if auditBefore.After(before) {
		return before
	}
	return auditBefore
}

func (r *Retention) Preview(ctx context.Context, before, auditBefore time.Time) (store.HistoryCounts, error) {
	return r.Store.CountHistory(ctx, before, clamp(before, auditBefore))
}

// ErrPartial means the history was deleted, but some log directories could not be
// removed or the database could not be compacted.
var ErrPartial = errors.New("retention: history deleted, but cleaning up after it failed")

// defaultVacuumMin is how many deleted rows make compacting the database worth it.
const defaultVacuumMin = 1000

// Clean deletes the history, then (best effort) the log files of the deleted
// environments, and compacts the database after large deletions.
func (r *Retention) Clean(ctx context.Context, before, auditBefore time.Time) (store.HistoryCounts, error) {
	c, envs, err := r.Store.DeleteHistory(ctx, before, clamp(before, auditBefore))
	if err != nil {
		return c, err
	}
	var errs []error
	for _, id := range envs {
		if err := r.Logs.RemoveEnvironment(id); err != nil {
			errs = append(errs, err)
		}
	}
	minRows := r.VacuumMin
	if minRows <= 0 {
		minRows = defaultVacuumMin
	}
	if c.Environments+c.Jobs+c.Events+c.AuditEvents+c.Templates >= minRows {
		vacuum := r.vacuum
		if vacuum == nil {
			vacuum = r.Store.Vacuum
		}
		// Not tied to the request: a closed browser must not stop it half-way.
		if err := vacuum(context.WithoutCancel(ctx)); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("%w: %w", ErrPartial, errors.Join(errs...))
	}
	return c, nil
}

// Sweep cleans with the configured days when the mode is automatic, and records the
// outcome as an event.
func (r *Retention) Sweep(ctx context.Context) (store.HistoryCounts, bool, error) {
	s, err := r.Settings(ctx)
	if err != nil || s.Mode != ModeAutomatic {
		return store.HistoryCounts{}, false, err
	}
	before, audit, _ := r.Cutoffs(ctx)
	c, err := r.Clean(ctx, before, audit)
	if r.Recorder != nil {
		if err != nil && !errors.Is(err, ErrPartial) {
			_, _ = r.Recorder.Error(ctx, "retention.failed", "history cleanup failed: "+err.Error(), events.Refs{}, nil)
		} else {
			_, _ = r.Recorder.Info(ctx, "retention.cleaned", "history cleaned: "+Describe(c), events.Refs{}, map[string]any{"counts": c})
			if err != nil {
				_, _ = r.Recorder.Warn(ctx, "retention.incomplete", err.Error(), events.Refs{}, nil)
			}
		}
	}
	return c, true, err
}

func (r *Retention) runOnce(ctx context.Context) { _, _, _ = r.Sweep(ctx) }

// Describe says how much a cleanup deleted.
func Describe(c store.HistoryCounts) string {
	return fmt.Sprintf("%d environments, %d jobs, %d events, %d audit events, %d templates", c.Environments, c.Jobs, c.Events, c.AuditEvents, c.Templates)
}

// nextRun is the next half past hour (local to now), strictly after now.
func nextRun(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 30, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Run sweeps every day until ctx ends.
func (r *Retention) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextRun(time.Now(), r.Hour))):
		}
		r.runOnce(ctx)
	}
}
