package retention

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type harness struct {
	r   *Retention
	db  *store.Store
	dir string
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &harness{db: db, dir: t.TempDir(), now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	h.r = &Retention{Store: db, Logs: logs.New(h.dir, db), Recorder: events.NewRecorder(db, events.NewBus(), nil), Now: func() time.Time { return h.now }}
	return h
}

// oldDestroyedEnv makes a destroyed environment 40 days old, with a log file.
func (h *harness) oldDestroyedEnv(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	at := h.now.Add(-40 * 24 * time.Hour)
	if err := h.db.CreateEnvironment(ctx, store.Environment{ID: id, ScaleSet: "ss", State: "destroyed", CreatedAt: at, UpdatedAt: at, StateChangedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := h.r.Logs.Write(ctx, id, "runner", "line", at); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsAndValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if s, err := h.r.Settings(ctx); err != nil || s != Defaults {
		t.Fatalf("settings = %+v, %v; want the defaults", s, err)
	}
	for _, bad := range []Settings{
		{Mode: "sometimes", Days: 30, AuditDays: 365},
		{Mode: ModeAutomatic, Days: 0, AuditDays: 365},
		{Mode: ModeAutomatic, Days: 366, AuditDays: 400},
		{Mode: ModeAutomatic, Days: 60, AuditDays: 30},
		{Mode: ModeAutomatic, Days: 30, AuditDays: 3651},
	} {
		if err := h.r.PutSettings(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", bad, err)
		}
	}
	want := Settings{Mode: ModeManual, Days: 7, AuditDays: 90}
	if err := h.r.PutSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.r.Settings(ctx); s != want {
		t.Fatalf("settings = %+v", s)
	}
}

func TestCleanRemovesRowsAndLogFiles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.oldDestroyedEnv(t, "env1")
	before, audit, _ := h.r.Cutoffs(ctx)
	if c, err := h.r.Preview(ctx, before, audit); err != nil || c.Environments != 1 {
		t.Fatalf("preview = %+v, %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "env1")); err != nil {
		t.Fatal("preview must not delete")
	}
	if c, err := h.r.Clean(ctx, before, audit); err != nil || c.Environments != 1 {
		t.Fatalf("clean = %+v, %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "env1")); !os.IsNotExist(err) {
		t.Fatal("log directory kept")
	}
}

func TestCleanToleratesMissingLogDirectories(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.oldDestroyedEnv(t, "env1")
	_ = os.RemoveAll(filepath.Join(h.dir, "env1"))
	before, audit, _ := h.r.Cutoffs(ctx)
	if _, err := h.r.Clean(ctx, before, audit); err != nil {
		t.Fatal(err)
	}
}

func TestCleanClampsAuditCutoff(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	at := h.now.Add(-10 * 24 * time.Hour)
	_, _ = h.db.AppendEvent(ctx, store.Event{Time: at, Kind: "audit.login", Level: "info", Message: "x"})
	// An audit cut-off later than the history cut-off is clamped to it.
	c, err := h.r.Clean(ctx, h.now.Add(-30*24*time.Hour), h.now)
	if err != nil || c.AuditEvents != 0 {
		t.Fatalf("clean = %+v, %v; a 10-day-old audit event must stay", c, err)
	}
}

func TestSweepRunsOnlyInAutomaticMode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.oldDestroyedEnv(t, "env1")
	_ = h.r.PutSettings(ctx, Settings{Mode: ModeManual, Days: 30, AuditDays: 365})
	if _, ran, err := h.r.Sweep(ctx); err != nil || ran {
		t.Fatalf("manual mode: ran=%v err=%v", ran, err)
	}
	_ = h.r.PutSettings(ctx, Defaults)
	c, ran, err := h.r.Sweep(ctx)
	if err != nil || !ran || c.Environments != 1 {
		t.Fatalf("automatic mode: %+v ran=%v err=%v", c, ran, err)
	}
	evs, _ := h.db.ListEvents(ctx, store.EventFilter{})
	found := false
	for _, e := range evs {
		found = found || e.Kind == "retention.cleaned"
	}
	if !found {
		t.Fatal("no retention.cleaned event")
	}
}

func TestRunSkipsInManualMode(t *testing.T) {
	// The mode is read at each run, so switching to manual stops the next sweep.
	h := newHarness(t)
	ctx := context.Background()
	h.oldDestroyedEnv(t, "env1")
	_ = h.r.PutSettings(ctx, Settings{Mode: ModeManual, Days: 30, AuditDays: 365})
	h.r.runOnce(ctx)
	if _, err := h.db.GetEnvironment(ctx, "env1"); err != nil {
		t.Fatal("manual mode deleted history")
	}
}

func TestNextRunIsHalfPastTheHour(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 8, h, m, 0, 0, time.UTC) }
	if got := nextRun(at(1, 0), 3); !got.Equal(at(3, 30)) {
		t.Fatalf("got %v", got)
	}
	if got := nextRun(at(3, 30), 3); !got.Equal(at(3, 30).Add(24 * time.Hour)) {
		t.Fatalf("got %v", got)
	}
}
