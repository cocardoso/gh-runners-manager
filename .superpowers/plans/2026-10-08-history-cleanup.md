# History Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator delete old history (environments, jobs, events, logs, failed/deleted template records) automatically or by hand, from the UI.

**Architecture:** Store functions delete by age in one transaction with the protection rules of spec A.2; a `retention` package owns the settings (meta table), previews, cleans (rows + log directories + VACUUM) and runs the daily sweep; the API exposes settings, cleanup and single deletes; the UI adds a History card, a cleanup dialog and Delete actions.

**Tech Stack:** Go 1.27, SQLite (modernc), huma v2; React + Kumo, TanStack Query, Vitest.

**Spec:** `.superpowers/specs/2026-10-08-history-cleanup-and-i18n-design.md` (part A)

## Global Constraints

- Defaults: mode `automatic`, history days `30` (1–365), audit days `365` (≥ history days, ≤ 3650).
- Audit events are those whose `kind` starts with `audit.`.
- Never delete: environments not `destroyed`; build/verify environments of templates not `failed`/`deleted`; templates not `failed`/`deleted`; scale sets, credentials, users, sessions, settings, secrets.
- Not deletable → `store.ErrConflict` → HTTP 409 with the reason; unknown → `store.ErrNotFound` → 404.
- Every manual deletion is audited (`audit(ctx, d, kind, msg, refs, data)` in `internal/api/auth.go`); every sweep records `retention.cleaned` / `retention.failed`.
- Everything in the repository in English; no process files committed.

## Review Focus

1. A cleanup while a job is running: its environment, job and logs must survive (state not `destroyed`). → Task 1 `TestDeleteHistoryKeepsLiveEnvironments`.
2. The build log of the active template must survive cleanups of any age. → Task 1 `TestDeleteHistoryKeepsTemplateEnvironments`.
3. A cleanup date in the future, or an audit cut-off later than the history cut-off, must be refused or clamped (never deletes recent audit trail). → Task 3 `TestHistoryCleanupRejectsTheFuture`, Task 2 `TestCleanClampsAuditCutoff`.
4. A missing `logs/<env>` directory (already removed, or never written) must not fail the cleanup. → Task 2 `TestCleanToleratesMissingLogDirectories`.
5. Switching to manual mode must stop the daily sweep immediately (no restart). → Task 2 `TestRunSkipsInManualMode`.

---

### Task 1: Store — history deletion rules

**Files:**
- Create: `internal/store/history.go`
- Test: `internal/store/history_test.go`

**Interfaces:**
- Produces:
  - `type HistoryCounts struct { Environments, Jobs, Events, AuditEvents, Templates int }` (json tags `environments, jobs, events, audit_events, templates`)
  - `func (s *Store) CountHistory(ctx context.Context, before, auditBefore time.Time) (HistoryCounts, error)`
  - `func (s *Store) DeleteHistory(ctx context.Context, before, auditBefore time.Time) (HistoryCounts, []string, error)` — returns the deleted environment ids
  - `func (s *Store) DeleteEnvironmentHistory(ctx context.Context, id string) error`
  - `func (s *Store) DeleteTemplateRecord(ctx context.Context, id string) error`
  - `func (s *Store) Vacuum(ctx context.Context) error`

- [ ] **Step 1: Write the failing tests** (`history_test.go`, package `store`, using `openTemp(t)` and direct SQL on `s.db` to set old timestamps)

```go
package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	now    = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	old    = now.Add(-40 * 24 * time.Hour)
	recent = now.Add(-2 * 24 * time.Hour)
	cutoff = now.Add(-30 * 24 * time.Hour)
	auditC = now.Add(-365 * 24 * time.Hour)
)

func seedEnv(t *testing.T, s *Store, id, state, kind string, changed time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateEnvironment(ctx, Environment{ID: id, ScaleSet: "ss", State: state, Kind: kind, CreatedAt: changed, UpdatedAt: changed, StateChangedAt: changed}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE environments SET state = ?, state_changed_at = ? WHERE id = ?`, state, ms(changed), id); err != nil {
		t.Fatal(err)
	}
	_ = s.UpsertLogStream(ctx, LogStream{EnvironmentID: id, Stream: "runner", Path: id + "/runner.log"})
}

func seedJob(t *testing.T, s *Store, id, env string, finished time.Time) {
	t.Helper()
	if err := s.UpsertJob(context.Background(), Job{ID: id, EnvironmentID: env, Status: "completed", FinishedAt: finished, UpdatedAt: finished}); err != nil {
		t.Fatal(err)
	}
}

func seedEvent(t *testing.T, s *Store, kind, env string, at time.Time) {
	t.Helper()
	if _, err := s.AppendEvent(context.Background(), Event{Time: at, Kind: kind, Level: "info", Message: kind, EnvironmentID: env}); err != nil {
		t.Fatal(err)
	}
}

func seedTemplate(t *testing.T, s *Store, id, state, buildEnv string, updated time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateTemplate(ctx, Template{ID: id, State: state, BuildEnvID: buildEnv, CreatedAt: updated, UpdatedAt: updated}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE templates SET updated_at = ? WHERE id = ?`, ms(updated), id); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteHistoryRemovesOldItems(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedEnv(t, s, "old-env", "destroyed", KindJob, old)
	seedEnv(t, s, "new-env", "destroyed", KindJob, recent)
	seedJob(t, s, "old-job", "old-env", old)
	seedJob(t, s, "new-job", "new-env", recent)
	seedEvent(t, s, "environment.state", "old-env", old)
	seedEvent(t, s, "environment.state", "new-env", recent)
	seedEvent(t, s, "audit.login", "", old)                     // within the audit window
	seedEvent(t, s, "audit.login", "", now.Add(-400*24*time.Hour)) // beyond it
	seedTemplate(t, s, "tpl-failed", TemplateFailed, "", old)
	seedTemplate(t, s, "tpl-failed-new", TemplateFailed, "", recent)

	want := HistoryCounts{Environments: 1, Jobs: 1, Events: 1, AuditEvents: 1, Templates: 1}
	if got, err := s.CountHistory(ctx, cutoff, auditC); err != nil || got != want {
		t.Fatalf("count = %+v, %v; want %+v", got, err, want)
	}
	got, envs, err := s.DeleteHistory(ctx, cutoff, auditC)
	if err != nil || got != want || len(envs) != 1 || envs[0] != "old-env" {
		t.Fatalf("delete = %+v %v %v; want %+v [old-env]", got, envs, err, want)
	}
	if _, err := s.GetEnvironment(ctx, "old-env"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old environment kept")
	}
	if _, err := s.GetEnvironment(ctx, "new-env"); err != nil {
		t.Fatal("recent environment deleted")
	}
	if _, err := s.GetJob(ctx, "old-job"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old job kept")
	}
	if streams, _ := s.ListLogStreams(ctx, "old-env"); len(streams) != 0 {
		t.Fatal("log streams of the deleted environment kept")
	}
	if _, err := s.GetTemplate(ctx, "tpl-failed"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old failed template kept")
	}
	if again, _ := s.CountHistory(ctx, cutoff, auditC); again != (HistoryCounts{}) {
		t.Fatalf("after delete = %+v", again)
	}
}

func TestDeleteHistoryKeepsLiveEnvironments(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedEnv(t, s, "running", "running", KindJob, old) // a long job
	seedJob(t, s, "its-job", "running", time.Time{})  // not finished
	seedEnv(t, s, "failed-kept", "failed", KindJob, old)
	got, _, err := s.DeleteHistory(ctx, now, auditC)
	if err != nil || got.Environments != 0 || got.Jobs != 0 {
		t.Fatalf("deleted %+v, %v; live environments and unfinished jobs stay", got, err)
	}
}

func TestDeleteHistoryKeepsTemplateEnvironments(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedEnv(t, s, "build-of-active", "destroyed", KindBuild, old)
	seedTemplate(t, s, "tpl-active", TemplateReady, "build-of-active", old)
	seedEnv(t, s, "build-of-failed", "destroyed", KindBuild, old)
	seedTemplate(t, s, "tpl-failed", TemplateFailed, "build-of-failed", old)
	_, envs, err := s.DeleteHistory(ctx, now, auditC)
	if err != nil || len(envs) != 1 || envs[0] != "build-of-failed" {
		t.Fatalf("deleted %v, %v; the kept template's build environment stays", envs, err)
	}
	if _, err := s.GetEnvironment(ctx, "build-of-active"); err != nil {
		t.Fatal("build environment of a kept template deleted")
	}
}

func TestDeleteEnvironmentHistory(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedEnv(t, s, "done", "destroyed", KindJob, recent)
	seedJob(t, s, "done-job", "done", recent)
	seedEvent(t, s, "environment.state", "done", recent)
	seedEnv(t, s, "live", "idle", KindJob, recent)
	seedEnv(t, s, "build", "destroyed", KindBuild, recent)
	seedTemplate(t, s, "tpl", TemplateActive, "build", recent)
	if err := s.DeleteEnvironmentHistory(ctx, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetJob(ctx, "done-job"); !errors.Is(err, ErrNotFound) {
		t.Fatal("job kept")
	}
	if evs, _ := s.ListEvents(ctx, EventFilter{EnvironmentID: "done"}); len(evs) != 0 {
		t.Fatal("events kept")
	}
	for id, want := range map[string]error{"live": ErrConflict, "build": ErrConflict, "nope": ErrNotFound} {
		if err := s.DeleteEnvironmentHistory(ctx, id); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", id, err, want)
		}
	}
}

func TestDeleteTemplateRecord(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedTemplate(t, s, "failed", TemplateFailed, "", recent)
	seedTemplate(t, s, "gone", TemplateDeleted, "", recent)
	seedTemplate(t, s, "ready", TemplateReady, "", recent)
	for _, id := range []string{"failed", "gone"} {
		if err := s.DeleteTemplateRecord(ctx, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if err := s.DeleteTemplateRecord(ctx, "ready"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ready: %v, want conflict", err)
	}
	if err := s.DeleteTemplateRecord(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}
```

(If `EventFilter` has no `EnvironmentID` field, use the field the existing filter has for it — check `internal/store/events.go` — or count rows with `SELECT COUNT(*) FROM events WHERE environment_id = ?`.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/store/ -run 'History|TemplateRecord'`
Expected: build failure — `CountHistory`, `DeleteHistory`, … undefined.

- [ ] **Step 3: Implement `history.go`**

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HistoryCounts is how much history a cleanup deletes (or would delete).
type HistoryCounts struct {
	Environments int `json:"environments"`
	Jobs         int `json:"jobs"`
	Events       int `json:"events"`
	AuditEvents  int `json:"audit_events"`
	Templates    int `json:"templates"`
}

// Environments whose logs are the build or verification record of a template that is
// still kept (not failed or deleted).
const keptTemplateEnvs = `SELECT build_env_id FROM templates WHERE state NOT IN ('failed','deleted') AND build_env_id != ''
	UNION SELECT verify_env_id FROM templates WHERE state NOT IN ('failed','deleted') AND verify_env_id != ''`

const oldEnvs = `SELECT id FROM environments WHERE state = 'destroyed' AND state_changed_at < ?1 AND id NOT IN (` + keptTemplateEnvs + `)`

const oldJobs = `SELECT id FROM jobs WHERE (finished_at != 0 AND finished_at < ?1) OR (environment_id != '' AND environment_id IN (` + oldEnvs + `))`

// querier is *sql.DB or *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func countHistory(ctx context.Context, q querier, before, auditBefore int64) (HistoryCounts, error) {
	var c HistoryCounts
	for _, x := range []struct {
		dst  *int
		sql  string
		args []any
	}{
		{&c.Environments, `SELECT COUNT(*) FROM (` + oldEnvs + `)`, []any{before}},
		{&c.Jobs, `SELECT COUNT(*) FROM (` + oldJobs + `)`, []any{before}},
		{&c.Events, `SELECT COUNT(*) FROM events WHERE ts < ? AND kind NOT LIKE 'audit.%'`, []any{before}},
		{&c.AuditEvents, `SELECT COUNT(*) FROM events WHERE ts < ? AND kind LIKE 'audit.%'`, []any{auditBefore}},
		{&c.Templates, `SELECT COUNT(*) FROM templates WHERE state IN ('failed','deleted') AND updated_at < ?`, []any{before}},
	} {
		if err := q.QueryRowContext(ctx, x.sql, x.args...).Scan(x.dst); err != nil {
			return HistoryCounts{}, fmt.Errorf("store: count history: %w", err)
		}
	}
	return c, nil
}

// CountHistory counts what DeleteHistory would delete.
func (s *Store) CountHistory(ctx context.Context, before, auditBefore time.Time) (HistoryCounts, error) {
	return countHistory(ctx, s.db, ms(before), ms(auditBefore))
}

// DeleteHistory deletes history older than before (audit events: auditBefore) and
// returns the counts and the ids of the deleted environments (their log files are the
// caller's to remove).
func (s *Store) DeleteHistory(ctx context.Context, before, auditBefore time.Time) (HistoryCounts, []string, error) {
	b, a := ms(before), ms(auditBefore)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HistoryCounts{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := countHistory(ctx, tx, b, a)
	if err != nil {
		return c, nil, err
	}
	rows, err := tx.QueryContext(ctx, oldEnvs, b)
	if err != nil {
		return c, nil, err
	}
	var envs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return c, nil, err
		}
		envs = append(envs, id)
	}
	rows.Close()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM jobs WHERE id IN (` + oldJobs + `)`, []any{b}},
		{`DELETE FROM events WHERE ts < ? AND kind NOT LIKE 'audit.%'`, []any{b}},
		{`DELETE FROM events WHERE ts < ? AND kind LIKE 'audit.%'`, []any{a}},
		{`DELETE FROM log_streams WHERE environment_id IN (` + oldEnvs + `)`, []any{b}},
		{`DELETE FROM environments WHERE id IN (` + oldEnvs + `)`, []any{b}},
		{`DELETE FROM templates WHERE state IN ('failed','deleted') AND updated_at < ?`, []any{b}},
	} {
		if _, err := tx.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			return c, nil, fmt.Errorf("store: delete history: %w", err)
		}
	}
	return c, envs, tx.Commit()
}

// DeleteEnvironmentHistory deletes one destroyed environment with its jobs, events and
// log streams. It is ErrConflict while the environment is not destroyed or its logs are
// a kept template's build or verification record.
func (s *Store) DeleteEnvironmentHistory(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM environments WHERE id = ?`, id).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "destroyed" {
		return fmt.Errorf("%w: environment %s is %s; only destroyed environments can be deleted", ErrConflict, id, state)
	}
	var kept int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+keptTemplateEnvs+`) WHERE build_env_id = ?`, id).Scan(&kept); err != nil {
		return err
	}
	if kept > 0 {
		return fmt.Errorf("%w: environment %s holds the build or verification logs of a kept template", ErrConflict, id)
	}
	for _, q := range []string{
		`DELETE FROM jobs WHERE environment_id = ?`,
		`DELETE FROM events WHERE environment_id = ?`,
		`DELETE FROM log_streams WHERE environment_id = ?`,
		`DELETE FROM environments WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteTemplateRecord deletes a failed or deleted template's record.
func (s *Store) DeleteTemplateRecord(ctx context.Context, id string) error {
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM templates WHERE id = ?`, id).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != TemplateFailed && state != TemplateDeleted {
		return fmt.Errorf("%w: template %s is %s; only failed or deleted records can be deleted", ErrConflict, id, state)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM templates WHERE id = ? AND state IN ('failed','deleted')`, id)
	return err
}

// Vacuum gives the space of deleted rows back to the file system.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return err
}
```

(The UNION's column is named `build_env_id`, so `WHERE build_env_id = ?` filters it.)

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit** — `feat(store): delete history by age, environments and template records`

---

### Task 2: Retention — settings, cleanup, daily sweep

**Files:**
- Modify: `internal/logs/logs.go` (add `RemoveEnvironment`)
- Create: `internal/retention/retention.go`
- Test: `internal/logs/logs_test.go` (one test), `internal/retention/retention_test.go`

**Interfaces:**
- Consumes: Task 1 store functions.
- Produces:
  - `func (s *logs.Store) RemoveEnvironment(envID string) error`
  - `package retention`: `type Settings struct { Mode string `json:"mode" enum:"automatic,manual"`; Days int `json:"days"`; AuditDays int `json:"audit_days"` }`, `const ModeAutomatic, ModeManual`, `var Defaults = Settings{ModeAutomatic, 30, 365}`, `var ErrInvalid`
  - `type Retention struct { Store *store.Store; Logs *logs.Store; Recorder *events.Recorder; Hour int; Now func() time.Time }`
  - `func (r *Retention) Settings(ctx) (Settings, error)`, `PutSettings(ctx, Settings) error`, `Cutoffs(ctx) (before, auditBefore time.Time, err error)`, `Preview(ctx, before, auditBefore) (store.HistoryCounts, error)`, `Clean(ctx, before, auditBefore) (store.HistoryCounts, error)`, `Sweep(ctx) (store.HistoryCounts, bool, error)` (bool: ran), `Run(ctx)`.

- [ ] **Step 1: Failing tests**

`internal/logs/logs_test.go`:

```go
func TestRemoveEnvironmentDeletesItsLogs(t *testing.T) {
	s, dir := newLogStore(t) // existing helper; otherwise logs.New(t.TempDir(), db)
	ctx := context.Background()
	if err := s.Write(ctx, "env1", "runner", "hello", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveEnvironment("env1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "env1")); !os.IsNotExist(err) {
		t.Fatal("log directory kept")
	}
	if err := s.RemoveEnvironment("env1"); err != nil {
		t.Fatalf("a missing directory is fine: %v", err)
	}
	if err := s.RemoveEnvironment("../x"); err == nil {
		t.Fatal("an invalid id must be refused")
	}
}
```

`internal/retention/retention_test.go`:

```go
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
```

- [ ] **Step 2: Run** `go test ./internal/logs/ ./internal/retention/` → build failures (undefined).

- [ ] **Step 3: Implement**

`logs.go`:

```go
// RemoveEnvironment deletes the log files of an environment (a missing directory is fine).
func (s *Store) RemoveEnvironment(envID string) error {
	if err := validate(envID, Streams[0]); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, envID))
}
```

`retention.go`:

```go
// Package retention deletes old history: finished environments with their jobs and
// logs, events, and failed or deleted template records, by hand or once a day.
package retention

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
}

func (r *Retention) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Retention) Settings(ctx context.Context) (Settings, error) {
	s := Defaults
	for key, dst := range map[string]any{"history.mode": &s.Mode, "history.days": &s.Days, "history.audit_days": &s.AuditDays} {
		v, err := r.Store.GetMeta(ctx, key)
		if errors.Is(err, store.ErrNotFound) {
			continue
		} else if err != nil {
			return s, err
		}
		switch p := dst.(type) {
		case *string:
			*p = v
		case *int:
			if n, err := strconv.Atoi(v); err == nil {
				*p = n
			}
		}
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
	for key, v := range map[string]string{"history.mode": s.Mode, "history.days": strconv.Itoa(s.Days), "history.audit_days": strconv.Itoa(s.AuditDays)} {
		if err := r.Store.PutMeta(ctx, key, v); err != nil {
			return err
		}
	}
	return nil
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

// Clean deletes the history, the log files of the deleted environments, and compacts
// the database when anything went.
func (r *Retention) Clean(ctx context.Context, before, auditBefore time.Time) (store.HistoryCounts, error) {
	c, envs, err := r.Store.DeleteHistory(ctx, before, clamp(before, auditBefore))
	if err != nil {
		return c, err
	}
	for _, id := range envs {
		if err := r.Logs.RemoveEnvironment(id); err != nil {
			return c, err
		}
	}
	if c != (store.HistoryCounts{}) {
		if err := r.Store.Vacuum(ctx); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Sweep cleans with the configured days when the mode is automatic.
func (r *Retention) Sweep(ctx context.Context) (store.HistoryCounts, bool, error) {
	s, err := r.Settings(ctx)
	if err != nil || s.Mode != ModeAutomatic {
		return store.HistoryCounts{}, false, err
	}
	before, audit, _ := r.Cutoffs(ctx)
	c, err := r.Clean(ctx, before, audit)
	return c, true, err
}

func (r *Retention) runOnce(ctx context.Context) {
	c, ran, err := r.Sweep(ctx)
	if r.Recorder == nil || (!ran && err == nil) {
		return
	}
	if err != nil {
		_, _ = r.Recorder.Error(ctx, "retention.failed", "history cleanup failed: "+err.Error(), events.Refs{}, nil)
		return
	}
	_, _ = r.Recorder.Info(ctx, "retention.cleaned", fmt.Sprintf("history cleaned: %d environments, %d jobs, %d events, %d audit events, %d templates",
		c.Environments, c.Jobs, c.Events, c.AuditEvents, c.Templates), events.Refs{}, map[string]any{"counts": c})
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
```

(Check `Recorder.Error`/`Recorder.Info` signatures in `internal/events`; they are used this way in `cmd/ghrm/serve.go` for backups.)

- [ ] **Step 4: Run** `go test -race ./internal/logs/ ./internal/retention/` → PASS.

- [ ] **Step 5: Commit** — `feat(retention): history settings, cleanup and the daily sweep`

---

### Task 3: API and wiring

**Files:**
- Create: `internal/api/history.go`
- Modify: `internal/api/api.go` (Deps `History *retention.Retention`; register), `internal/api/templates.go` (DELETE template), `internal/api/api.go` or the environments file (DELETE environment), `cmd/ghrm/serve.go` (construct and run), `internal/demo/demo.go` (demo has History too)
- Test: `internal/api/history_test.go`

**Interfaces:**
- Consumes: Task 2 `retention.Retention`, Task 1 store deletes.
- Produces: `GET/PUT /api/v1/history/settings` (body `retention.Settings`), `POST /api/v1/history/cleanup` body `{before: time, audit_before?: time, dry_run: bool}` → `store.HistoryCounts`, `DELETE /api/v1/templates/{id}` (204), `DELETE /api/v1/environments/{id}` (204). Events: `template.record_deleted`, `environment.record_deleted`, audit kinds `history_settings`, `history_cleanup`, `template_delete`, `environment_delete`.

- [ ] **Step 1: Failing tests** (`history_test.go`, using `newHarnessWith`, `h.call`, `harnessToken` as the other API tests do)

```go
func historyHarness(t *testing.T) *harness {
	return newHarnessWith(t, "", func(d *Deps) {
		d.History = &retention.Retention{Store: d.Store, Logs: d.Logs, Recorder: d.Recorder}
	})
}

func TestHistorySettingsRoundTrip(t *testing.T) {
	h := historyHarness(t)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.call(t, "GET", "/api/v1/history/settings", nil, tok)
	if code != 200 || !strings.Contains(string(b), `"mode":"automatic"`) || !strings.Contains(string(b), `"days":30`) {
		t.Fatalf("%d %s", code, b)
	}
	code, b = h.call(t, "PUT", "/api/v1/history/settings", map[string]any{"mode": "manual", "days": 7, "audit_days": 3}, tok)
	if code != 422 && code != 400 {
		t.Fatalf("audit < days accepted: %d %s", code, b)
	}
	code, _ = h.call(t, "PUT", "/api/v1/history/settings", map[string]any{"mode": "manual", "days": 7, "audit_days": 90}, tok)
	if code != 204 {
		t.Fatalf("put = %d", code)
	}
	if _, b = h.call(t, "GET", "/api/v1/history/settings", nil, tok); !strings.Contains(string(b), `"mode":"manual"`) {
		t.Fatalf("%s", b)
	}
}

func TestHistoryCleanupDryRunAndRun(t *testing.T) {
	h := historyHarness(t)
	ctx := context.Background()
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	old := time.Now().Add(-40 * 24 * time.Hour)
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "e1", ScaleSet: "ss", State: "destroyed", CreatedAt: old, UpdatedAt: old, StateChangedAt: old})
	before := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	code, b := h.call(t, "POST", "/api/v1/history/cleanup", map[string]any{"before": before, "dry_run": true}, tok)
	if code != 200 || !strings.Contains(string(b), `"environments":1`) {
		t.Fatalf("dry run: %d %s", code, b)
	}
	if _, err := h.db.GetEnvironment(ctx, "e1"); err != nil {
		t.Fatal("dry run deleted")
	}
	if code, b = h.call(t, "POST", "/api/v1/history/cleanup", map[string]any{"before": before}, tok); code != 200 || !strings.Contains(string(b), `"environments":1`) {
		t.Fatalf("run: %d %s", code, b)
	}
	if _, err := h.db.GetEnvironment(ctx, "e1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("not deleted")
	}
	if !hasEvent(t, h.db, "audit.history_cleanup") {
		t.Fatal("cleanup not audited")
	}
}

func TestHistoryCleanupRejectsTheFuture(t *testing.T) {
	h := historyHarness(t)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, b := h.call(t, "POST", "/api/v1/history/cleanup", map[string]any{"before": future}, tok); code != 422 && code != 400 {
		t.Fatalf("future cut-off accepted: %d %s", code, b)
	}
}

func TestDeleteTemplateAndEnvironmentRecords(t *testing.T) {
	h := historyHarness(t)
	ctx := context.Background()
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	now := time.Now()
	_ = h.db.CreateTemplate(ctx, store.Template{ID: "tf", State: store.TemplateFailed, CreatedAt: now, UpdatedAt: now})
	_ = h.db.CreateTemplate(ctx, store.Template{ID: "ta", State: store.TemplateReady, CreatedAt: now, UpdatedAt: now})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "ed", ScaleSet: "ss", State: "destroyed", CreatedAt: now, UpdatedAt: now, StateChangedAt: now})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "el", ScaleSet: "ss", State: "idle", CreatedAt: now, UpdatedAt: now, StateChangedAt: now})
	for path, want := range map[string]int{
		"/api/v1/templates/tf": 204, "/api/v1/templates/ta": 409, "/api/v1/templates/nope": 404,
		"/api/v1/environments/ed": 204, "/api/v1/environments/el": 409, "/api/v1/environments/nope": 404,
	} {
		if code, b := h.call(t, "DELETE", path, nil, tok); code != want {
			t.Errorf("DELETE %s = %d %s, want %d", path, code, b, want)
		}
	}
	if !hasEvent(t, h.db, "audit.template_delete") || !hasEvent(t, h.db, "audit.environment_delete") {
		t.Fatal("deletes not audited")
	}
}

func hasEvent(t *testing.T, db *store.Store, kind string) bool {
	t.Helper()
	evs, _ := db.ListEvents(context.Background(), store.EventFilter{})
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
```

(`h.call` signature, the harness field names `db`/`logs`/`rec`, and how the audit helper prefixes `audit.` are in `api_test.go`/`auth.go`; adjust names to match, not the assertions.)

- [ ] **Step 2: Run** `go test ./internal/api/ -run 'History|Records'` → build failure.

- [ ] **Step 3: Implement `history.go`**

```go
package api

// registerHistory adds the history settings, cleanup and record deletion endpoints.
func registerHistory(a huma.API, d Deps) {
	tags := []string{"history"}
	unavailable := func() error { return huma.Error503ServiceUnavailable("history cleanup is not configured") }
	huma.Register(a, huma.Operation{OperationID: "get-history-settings", Method: http.MethodGet, Path: "/api/v1/history/settings",
		Summary: "How long history is kept, and whether it is cleaned every day", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body retention.Settings }, error) {
			if d.History == nil {
				return nil, unavailable()
			}
			s, err := d.History.Settings(ctx)
			return &struct{ Body retention.Settings }{s}, err
		})
	huma.Register(a, huma.Operation{OperationID: "put-history-settings", Method: http.MethodPut, Path: "/api/v1/history/settings",
		Summary: "Change the history settings", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct{ Body retention.Settings }) (*struct{}, error) {
			if d.History == nil {
				return nil, unavailable()
			}
			if err := d.History.PutSettings(ctx, in.Body); err != nil {
				if errors.Is(err, retention.ErrInvalid) {
					return nil, huma.Error422UnprocessableEntity(err.Error())
				}
				return nil, err
			}
			audit(ctx, d, "history_settings", fmt.Sprintf("history settings changed by %s: %s, %d days, audit %d days", Actor(ctx), in.Body.Mode, in.Body.Days, in.Body.AuditDays),
				events.Refs{}, map[string]any{"mode": in.Body.Mode, "days": in.Body.Days, "audit_days": in.Body.AuditDays})
			return &struct{}{}, nil
		})
	type cleanupIn struct {
		Body struct {
			Before      time.Time  `json:"before" doc:"Delete history older than this"`
			AuditBefore *time.Time `json:"audit_before,omitempty" doc:"Delete audit events older than this (default: the audit retention, never later than before)"`
			DryRun      bool       `json:"dry_run,omitempty" doc:"Only count"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "cleanup-history", Method: http.MethodPost, Path: "/api/v1/history/cleanup",
		Summary: "Delete (or count) history older than a date", Tags: tags},
		func(ctx context.Context, in *cleanupIn) (*struct{ Body store.HistoryCounts }, error) {
			if d.History == nil {
				return nil, unavailable()
			}
			if !in.Body.Before.Before(time.Now()) {
				return nil, huma.Error422UnprocessableEntity("before must be in the past")
			}
			_, audit0, err := d.History.Cutoffs(ctx)
			if err != nil {
				return nil, err
			}
			if in.Body.AuditBefore != nil {
				audit0 = *in.Body.AuditBefore
			}
			if in.Body.DryRun {
				c, err := d.History.Preview(ctx, in.Body.Before, audit0)
				return &struct{ Body store.HistoryCounts }{c}, err
			}
			c, err := d.History.Clean(ctx, in.Body.Before, audit0)
			if err != nil {
				return nil, err
			}
			audit(ctx, d, "history_cleanup", fmt.Sprintf("history before %s deleted by %s: %d environments, %d jobs, %d events, %d audit events, %d templates",
				in.Body.Before.UTC().Format(time.DateOnly), Actor(ctx), c.Environments, c.Jobs, c.Events, c.AuditEvents, c.Templates), events.Refs{}, map[string]any{"counts": c})
			return &struct{ Body store.HistoryCounts }{c}, nil
		})
	type idPath struct {
		ID string `path:"id"`
	}
	recordError := func(err error) error {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return huma.Error404NotFound("not found")
		case errors.Is(err, store.ErrConflict):
			return huma.Error409Conflict(strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": "))
		}
		return err
	}
	huma.Register(a, huma.Operation{OperationID: "delete-template-record", Method: http.MethodDelete, Path: "/api/v1/templates/{id}",
		Summary: "Delete a failed or deleted template's record", Tags: []string{"templates"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := d.Store.DeleteTemplateRecord(ctx, in.ID); err != nil {
				return nil, recordError(err)
			}
			audit(ctx, d, "template_delete", "template record "+in.ID+" deleted by "+Actor(ctx), events.Refs{}, map[string]any{"template": in.ID})
			return &struct{}{}, nil
		})
	huma.Register(a, huma.Operation{OperationID: "delete-environment-record", Method: http.MethodDelete, Path: "/api/v1/environments/{id}",
		Summary: "Delete a destroyed environment with its jobs, events and logs", Tags: []string{"environments"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := d.Store.DeleteEnvironmentHistory(ctx, in.ID); err != nil {
				return nil, recordError(err)
			}
			if d.Logs != nil {
				_ = d.Logs.RemoveEnvironment(in.ID)
			}
			audit(ctx, d, "environment_delete", "environment "+in.ID+" deleted from history by "+Actor(ctx), events.Refs{}, map[string]any{"environment": in.ID})
			return &struct{}{}, nil
		})
}
```

Register it next to the other `register…` calls in `api.go`; add `History *retention.Retention` to `Deps` with a doc comment ("cleans old history (nil: unavailable)"). Write endpoints are already behind the admin/session middleware like the other writes — verify in `api.go` that `DELETE` and `PUT`/`POST` on these paths require auth (the test calls with the bearer token; add one call without it expecting 401).

Wire in `cmd/ghrm/serve.go` after the backup block:

```go
hist := &retention.Retention{Store: db, Logs: logStore, Recorder: rec, Hour: cfg.Backup.AtHour()}
go hist.Run(ctx)
```

and pass `History: hist` in the `api.Deps`. Do the same in `internal/demo/demo.go` (without `Run`).

- [ ] **Step 4: Run** `go test -race ./internal/api/ ./cmd/... ./internal/demo/` → PASS; `make web-api` regenerates `web/src/api/schema.d.ts`.

- [ ] **Step 5: Commit** — `feat(api): history settings, cleanup and record deletion`

---

### Task 4: UI — History card, cleanup dialog, Delete actions

**Files:**
- Create: `web/src/components/history-card.tsx`, `web/src/components/delete-record.tsx`
- Modify: `web/src/api/queries.ts` (`useHistorySettings`), `web/src/pages/settings.tsx` (render the card), `web/src/pages/templates.tsx` and `web/src/pages/template-detail.tsx` (Delete on failed/deleted), `web/src/pages/environment-detail.tsx` (Delete from history on destroyed), `web/src/lib/event-stream.ts` (invalidate on `retention.`, `audit.template_delete`, `audit.environment_delete`, `audit.history_cleanup`), `web/src/test/api-mock.ts` (default `/api/v1/history/settings`)
- Test: `web/src/components/history-card.test.tsx`, additions to `templates.test.tsx`, `environments.test.tsx`

**Interfaces:**
- Consumes: Task 3 endpoints (typed via `schema.d.ts`: `api.GET("/api/v1/history/settings")`, `api.PUT(...)`, `api.POST("/api/v1/history/cleanup", { body })`, `api.DELETE("/api/v1/templates/{id}")`, `api.DELETE("/api/v1/environments/{id}")`).
- Produces: `useHistorySettings()`; `<HistoryCard/>`; `<DeleteRecord kind="template"|"environment" id={…} onDeleted={…}/>`.

- [ ] **Step 1: Failing tests**

`history-card.test.tsx`:

```tsx
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderApp } from "@/test/render-app";
import { mockApi } from "@/test/api-mock";
import { FakeEventSource } from "@/test/fake-event-source";

beforeEach(() => FakeEventSource.reset());
afterEach(() => vi.unstubAllGlobals());

test("the history card saves the mode and the days", async () => {
  const calls = mockApi({ "/api/v1/history/settings": { mode: "automatic", days: 30, audit_days: 365 } });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = (await screen.findByRole("heading", { name: "History" })).closest("section")!;
  await user.click(await within(card).findByLabelText("Manual"));
  const days = within(card).getByLabelText("Keep history (days)");
  await user.clear(days);
  await user.type(days, "7");
  await user.click(within(card).getByRole("button", { name: "Save" }));
  expect(calls.find((c) => c.method === "PUT" && c.path === "/api/v1/history/settings")?.body).toEqual({ mode: "manual", days: 7, audit_days: 365 });
});

test("clean up now previews the counts before deleting", async () => {
  const calls = mockApi({
    "/api/v1/history/settings": { mode: "manual", days: 30, audit_days: 365 },
    "POST /api/v1/history/cleanup": { environments: 3, jobs: 4, events: 50, audit_events: 0, templates: 1 },
  });
  const user = userEvent.setup();
  renderApp("/settings");
  const card = (await screen.findByRole("heading", { name: "History" })).closest("section")!;
  await user.click(within(card).getByRole("button", { name: /Clean up now/ }));
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText(/3 environments/)).toBeInTheDocument();
  expect(calls.filter((c) => c.path === "/api/v1/history/cleanup").map((c) => c.body.dry_run)).toEqual([true]);
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));
  expect(calls.filter((c) => c.path === "/api/v1/history/cleanup").map((c) => !!c.body.dry_run)).toEqual([true, false]);
});
```

`templates.test.tsx` (add):

```tsx
test("failed template records can be deleted; kept ones cannot", async () => {
  const failed = { ...templates[0], id: "tplfail", state: "failed" };
  const calls = mockApi({ "/api/v1/templates/tplfail": failed, "/api/v1/settings": settings, "DELETE /api/v1/templates/tplfail": null });
  const user = userEvent.setup();
  renderApp("/templates/tplfail");
  await user.click(await screen.findByRole("button", { name: "Delete" }));
  await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete" }));
  expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/v1/templates/tplfail")).toBe(true);
});

test("a ready template has no Delete action", async () => {
  mockApi({ "/api/v1/templates/tplnew": templates[0], "/api/v1/settings": settings });
  renderApp("/templates/tplnew");
  await screen.findByText(templates[0].slim_release);
  expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
});
```

`environments.test.tsx` (add): a `destroyed` environment detail shows **Delete from history** and calls `DELETE /api/v1/environments/{id}` after confirming; an `idle` one does not show it.

(Check `web/src/test/api-mock.ts` for how calls are recorded and how methods are keyed; extend it if it records neither method nor body — that extension is part of this step.)

- [ ] **Step 2: Run** `pnpm -C web exec vitest run src/components/history-card.test.tsx src/pages/templates.test.tsx src/pages/environments.test.tsx` → FAIL.

- [ ] **Step 3: Implement**
  - `useHistorySettings` in `queries.ts`: `useQuery({ queryKey: ["history-settings"], queryFn: async () => unwrap(await api.GET("/api/v1/history/settings")) })`.
  - `HistoryCard`: a `section` with `LayerCard` (same structure as `cache-card.tsx`), a heading "History"; radio group (Kumo `Radio`/`RadioGroup` — check `npx @cloudflare/kumo doc Radio`) "Automatic" / "Manual" with a one-line explanation each ("History older than the period below is deleted every day" / "Nothing is deleted until you clean up"); number inputs "Keep history (days)" (1–365) and "Keep audit events (days)"; Save via `useAdminAction().run("History settings saved", () => api.PUT(...))` then invalidate `["history-settings"]`; button "Clean up now…" opening a Kumo `Dialog` with a date input "Delete history before" (default today − days), a live preview (`POST … dry_run: true`, rerun when the date changes) rendered as "3 environments, 4 jobs, 50 events, 0 audit events, 1 template", and a destructive "Delete" button (disabled when the preview is all zero) which posts without `dry_run`, toasts "History deleted" with the counts, closes, and invalidates `templates`, `environments`, `jobs`, `events`.
  - `DeleteRecord`: a ghost destructive button ("Delete" for templates, "Delete from history" for environments) opening a confirmation dialog (title "Delete this record?", text explaining what goes: template — "Only the record is deleted; nothing changes on Proxmox. If it failed, the next check may build it again."; environment — "Its jobs, events and logs are deleted too."). On confirm it calls `api.DELETE`, shows API errors in the dialog, navigates back to the list (`useNavigate`) and invalidates the queries.
  - Render `DeleteRecord` on template detail and in the list row actions when `state` is `failed` or `deleted`; on environment detail when `state === "destroyed"`.
  - `event-stream.ts`: on events whose kind starts with `retention.` or equals `audit.history_cleanup`, `audit.template_delete`, `audit.environment_delete`, invalidate `templates`, `environments`, `jobs`, `events`, `overview`.
- [ ] **Step 4: Run** `make web-check` → PASS (lint, typecheck, all tests).

- [ ] **Step 5: Commit** — `feat(web): history settings, cleanup dialog and record deletion`

---

### Task 5: Docs, e2e, screenshots, PR and release

**Files:**
- Modify: `docs/architecture.md` (components table row "History retention and cleanup | `internal/retention`, `internal/store/history.go`"; mention the daily sweep where backups are described), `docs/development.md` (section "History": what is deleted, protections, settings, API), `web/e2e/settings.spec.ts` (History card saves), screenshots.

- [ ] **Step 1:** Add the e2e test (Playwright against `ghrm demo`): open Settings, switch History to Manual, save, reload, still Manual. Run `cd web && pnpm build && pnpm e2e` → PASS.
- [ ] **Step 2:** Docs updated; `pnpm screenshots` (Settings shows the History card); check `docs/images/settings-light.png`.
- [ ] **Step 3:** Full suite: `make check && make web-check && bash deploy/proxmox/test/run.sh`.
- [ ] **Step 4:** Commit `docs: history cleanup`; final review (opus) on the branch; fix all findings with RED→GREEN tests.
- [ ] **Step 5:** PR → CI → merge → tag `v0.2.0` → release → `bash install.sh …` on the production host → in the UI: delete the failed template record of 2026-10-08, preview a cleanup.
