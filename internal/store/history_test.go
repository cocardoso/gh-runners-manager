package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	hNow    = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	hOld    = hNow.Add(-40 * 24 * time.Hour)
	hRecent = hNow.Add(-2 * 24 * time.Hour)
	hCutoff = hNow.Add(-30 * 24 * time.Hour)
	hAudit  = hNow.Add(-365 * 24 * time.Hour)
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
	seedEnv(t, s, "hOld-env", "destroyed", KindJob, hOld)
	seedEnv(t, s, "new-env", "destroyed", KindJob, hRecent)
	seedJob(t, s, "hOld-job", "hOld-env", hOld)
	seedJob(t, s, "new-job", "new-env", hRecent)
	seedEvent(t, s, "environment.state", "hOld-env", hOld)
	seedEvent(t, s, "environment.state", "new-env", hRecent)
	seedEvent(t, s, "audit.login", "", hOld)                        // within the audit window
	seedEvent(t, s, "audit.login", "", hNow.Add(-400*24*time.Hour)) // beyond it
	seedTemplate(t, s, "tpl-failed", TemplateFailed, "", hOld)
	seedTemplate(t, s, "tpl-failed-new", TemplateFailed, "", hRecent)

	want := HistoryCounts{Environments: 1, Jobs: 1, Events: 1, AuditEvents: 1, Templates: 1}
	if got, err := s.CountHistory(ctx, hCutoff, hAudit); err != nil || got != want {
		t.Fatalf("count = %+v, %v; want %+v", got, err, want)
	}
	got, envs, err := s.DeleteHistory(ctx, hCutoff, hAudit)
	if err != nil || got != want || len(envs) != 1 || envs[0] != "hOld-env" {
		t.Fatalf("delete = %+v %v %v; want %+v [hOld-env]", got, envs, err, want)
	}
	if _, err := s.GetEnvironment(ctx, "hOld-env"); !errors.Is(err, ErrNotFound) {
		t.Fatal("hOld environment kept")
	}
	if _, err := s.GetEnvironment(ctx, "new-env"); err != nil {
		t.Fatal("hRecent environment deleted")
	}
	if _, err := s.GetJob(ctx, "hOld-job"); !errors.Is(err, ErrNotFound) {
		t.Fatal("hOld job kept")
	}
	if streams, _ := s.ListLogStreams(ctx, "hOld-env"); len(streams) != 0 {
		t.Fatal("log streams of the deleted environment kept")
	}
	if _, err := s.GetTemplate(ctx, "tpl-failed"); !errors.Is(err, ErrNotFound) {
		t.Fatal("hOld failed template kept")
	}
	if again, _ := s.CountHistory(ctx, hCutoff, hAudit); again != (HistoryCounts{}) {
		t.Fatalf("after delete = %+v", again)
	}
}

func TestDeleteHistoryKeepsLiveEnvironments(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedEnv(t, s, "running", "running", KindJob, hOld) // a long job
	seedJob(t, s, "its-job", "running", time.Time{})   // not finished
	seedEnv(t, s, "failed-kept", "failed", KindJob, hOld)
	got, _, err := s.DeleteHistory(ctx, hNow, hAudit)
	if err != nil || got.Environments != 0 || got.Jobs != 0 {
		t.Fatalf("deleted %+v, %v; live environments and unfinished jobs stay", got, err)
	}
}

func TestDeleteHistoryKeepsTemplateEnvironments(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seedEnv(t, s, "build-of-active", "destroyed", KindBuild, hOld)
	seedTemplate(t, s, "tpl-active", TemplateReady, "build-of-active", hOld)
	seedEnv(t, s, "build-of-failed", "destroyed", KindBuild, hOld)
	seedTemplate(t, s, "tpl-failed", TemplateFailed, "build-of-failed", hOld)
	_, envs, err := s.DeleteHistory(ctx, hNow, hAudit)
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
	seedEnv(t, s, "done", "destroyed", KindJob, hRecent)
	seedJob(t, s, "done-job", "done", hRecent)
	seedEvent(t, s, "environment.state", "done", hRecent)
	seedEnv(t, s, "live", "idle", KindJob, hRecent)
	seedEnv(t, s, "build", "destroyed", KindBuild, hRecent)
	seedTemplate(t, s, "tpl", TemplateActive, "build", hRecent)
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
	seedTemplate(t, s, "failed", TemplateFailed, "", hRecent)
	seedTemplate(t, s, "gone", TemplateDeleted, "", hRecent)
	seedTemplate(t, s, "ready", TemplateReady, "", hRecent)
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
