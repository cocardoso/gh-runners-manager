package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenMigratesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghrm.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.SchemaVersion(context.Background())
	if err != nil || v1 < 1 {
		t.Fatalf("schema version = %d, %v", v1, err)
	}
	_ = s.Close()
	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	v2, _ := s2.SchemaVersion(context.Background())
	if v2 != v1 {
		t.Fatalf("schema version changed on reopen: %d -> %d", v1, v2)
	}
}

func env(id, scaleSet, state string, created time.Time) Environment {
	return Environment{ID: id, ScaleSet: scaleSet, State: state, CreatedAt: created, UpdatedAt: created, StateChangedAt: created}
}

func TestEnvironmentCRUDAndFilter(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, e := range []Environment{env("a", "x", "running", t0), env("b", "x", "destroyed", t0.Add(time.Second)), env("c", "y", "idle", t0.Add(2*time.Second))} {
		e.MemoryMB = 1024 * (i + 1)
		if err := s.CreateEnvironment(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetEnvironment(ctx, "a")
	if err != nil || got.State != "running" || got.MemoryMB != 1024 || !got.CreatedAt.Equal(t0) {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if _, err := s.GetEnvironment(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
	live, _ := s.ListEnvironments(ctx, EnvironmentFilter{States: []string{"running", "idle"}})
	if len(live) != 2 || live[0].ID != "c" || live[1].ID != "a" {
		t.Fatalf("live = %+v, want c then a (newest first)", ids(live))
	}
	xs, _ := s.ListEnvironments(ctx, EnvironmentFilter{ScaleSet: "x", Limit: 1})
	if len(xs) != 1 || xs[0].ID != "b" {
		t.Fatalf("scale set x limit 1 = %v", ids(xs))
	}
}

func ids(es []Environment) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestTransitionEnvironmentCompareAndSet(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.CreateEnvironment(ctx, env("a", "x", "pending", t0)); err != nil {
		t.Fatal(err)
	}
	e, err := s.TransitionEnvironment(ctx, "a", []string{"pending"}, "provisioning", func(e *Environment) { e.RuntimeRef = "900/a" })
	if err != nil || e.State != "provisioning" || e.RuntimeRef != "900/a" || !e.StateChangedAt.After(t0) {
		t.Fatalf("transition = %+v, %v", e, err)
	}
	if _, err := s.TransitionEnvironment(ctx, "a", []string{"pending"}, "booting", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong from = %v, want ErrConflict", err)
	}
	if _, err := s.TransitionEnvironment(ctx, "a", nil, "running", nil); err == nil {
		t.Fatal("provisioning -> running must be rejected by the state machine")
	}

	if err := s.CreateEnvironment(ctx, env("b", "x", "pending", t0)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.TransitionEnvironment(ctx, "b", []string{"pending"}, "provisioning", nil); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent transitions won = %d, want exactly 1", wins)
	}
}

func TestUpdateEnvironmentKeepsState(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	_ = s.CreateEnvironment(ctx, env("a", "x", "idle", time.Now()))
	code := 3
	e, err := s.UpdateEnvironment(ctx, "a", func(e *Environment) { e.IP = "10.0.0.5"; e.ExitCode = &code; e.State = "running" })
	if err != nil || e.IP != "10.0.0.5" || e.ExitCode == nil || *e.ExitCode != 3 || e.State != "idle" {
		t.Fatalf("update = %+v, %v (state must not change through UpdateEnvironment)", e, err)
	}
}

func TestUpsertJobMergesFields(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.UpsertJob(ctx, Job{ID: "j1", Repository: "owner/repo", RunID: 7, QueuedAt: t0, Status: "assigned"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertJob(ctx, Job{ID: "j1", RunnerName: "ghrm-abc", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, "j1")
	if err != nil || j.Repository != "owner/repo" || j.RunnerName != "ghrm-abc" || j.RunID != 7 || j.Status != "running" || !j.QueuedAt.Equal(t0) {
		t.Fatalf("job = %+v, %v", j, err)
	}
	list, _ := s.ListJobs(ctx, JobFilter{Status: "running"})
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
}

func TestAppendEventAssignsIncreasingSeqAndRoundTripsData(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	a, err := s.AppendEvent(ctx, Event{Time: time.Now(), Kind: "k", Level: "info", Message: "one", EnvironmentID: "e1", Data: map[string]any{"n": 1.0, "s": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.AppendEvent(ctx, Event{Time: time.Now(), Kind: "k", Level: "info", Message: "two", EnvironmentID: "e2"})
	if b.Seq <= a.Seq {
		t.Fatalf("seq %d then %d, want increasing", a.Seq, b.Seq)
	}
	after, _ := s.ListEvents(ctx, EventFilter{AfterSeq: a.Seq})
	if len(after) != 1 || after[0].Message != "two" {
		t.Fatalf("after = %+v", after)
	}
	byEnv, _ := s.ListEvents(ctx, EventFilter{EnvironmentID: "e1"})
	if len(byEnv) != 1 || byEnv[0].Data["s"] != "x" || byEnv[0].Data["n"] != 1.0 {
		t.Fatalf("byEnv = %+v", byEnv)
	}
}

func TestLogStreamUpsert(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ls := LogStream{EnvironmentID: "e1", Stream: "job", Path: "/x/job.log", LastSeq: 3, Bytes: 30, Lines: 3, FirstAt: time.Unix(1, 0), LastAt: time.Unix(2, 0)}
	if err := s.UpsertLogStream(ctx, ls); err != nil {
		t.Fatal(err)
	}
	ls.LastSeq, ls.Lines = 5, 5
	_ = s.UpsertLogStream(ctx, ls)
	got, err := s.GetLogStream(ctx, "e1", "job")
	if err != nil || got.LastSeq != 5 || got.Lines != 5 {
		t.Fatalf("got = %+v, %v", got, err)
	}
	all, _ := s.ListLogStreams(ctx, "e1")
	if len(all) != 1 {
		t.Fatalf("all = %+v", all)
	}
	if _, err := s.GetLogStream(ctx, "e1", "runner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stream = %v", err)
	}
}

func TestScaleSetRecord(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.GetScaleSet(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("want ErrNotFound")
	}
	_ = s.PutScaleSet(ctx, ScaleSetRecord{Name: "x", GitHubID: 42, URL: "https://github.com/o/r"})
	r, err := s.GetScaleSet(ctx, "x")
	if err != nil || r.GitHubID != 42 {
		t.Fatalf("record = %+v, %v", r, err)
	}
}

func TestListEventsNewestAndBefore(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.AppendEvent(ctx, Event{Kind: "k", Level: "info", Message: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	newest, err := s.ListEvents(ctx, EventFilter{Newest: true, Limit: 2})
	if err != nil || len(newest) != 2 || newest[0].Seq != 4 || newest[1].Seq != 5 {
		t.Fatalf("newest = %+v, %v; want seq 4, 5 in ascending order", newest, err)
	}
	before, _ := s.ListEvents(ctx, EventFilter{BeforeSeq: 4, Newest: true, Limit: 2})
	if len(before) != 2 || before[0].Seq != 2 || before[1].Seq != 3 {
		t.Fatalf("before 4 = %+v; want seq 2, 3", before)
	}
	window, _ := s.ListEvents(ctx, EventFilter{AfterSeq: 1, BeforeSeq: 4})
	if len(window) != 2 || window[0].Seq != 2 {
		t.Fatalf("window = %+v; want seq 2, 3", window)
	}
}

func TestSecretsAndMeta(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.GetSecret(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing secret: %v", err)
	}
	if err := s.PutSecret(ctx, "b", []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	_ = s.PutSecret(ctx, "a", []byte{3})
	_ = s.PutSecret(ctx, "a", []byte{4})
	if got, err := s.GetSecret(ctx, "a"); err != nil || len(got) != 1 || got[0] != 4 {
		t.Fatalf("a = %v, %v", got, err)
	}
	if names, _ := s.ListSecretNames(ctx); strings.Join(names, ",") != "a,b" {
		t.Fatalf("names = %v", names)
	}
	_ = s.DeleteSecret(ctx, "a")
	if _, err := s.GetSecret(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted secret still there")
	}
	if _, err := s.GetMeta(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing meta: %v", err)
	}
	_ = s.PutMeta(ctx, "k", "v1")
	_ = s.PutMeta(ctx, "k", "v2")
	if v, err := s.GetMeta(ctx, "k"); v != "v2" || err != nil {
		t.Fatalf("meta = %q, %v", v, err)
	}
}

func TestUsersAndSessions(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	now := time.UnixMilli(time.Now().UnixMilli())
	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("users = %d", n)
	}
	u := User{ID: "u1", Username: "admin", PasswordHash: "h1", CreatedAt: now, PasswordChangedAt: now}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, User{ID: "u2", Username: "admin", PasswordHash: "h"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: %v", err)
	}
	if got, err := s.GetUserByName(ctx, "admin"); err != nil || got.ID != "u1" || got.PasswordHash != "h1" {
		t.Fatalf("by name = %+v, %v", got, err)
	}
	if _, err := s.GetUserByName(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	_ = s.UpdatePassword(ctx, "u1", "h2", now.Add(time.Minute))
	if got, _ := s.GetUser(ctx, "u1"); got.PasswordHash != "h2" || !got.PasswordChangedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after update = %+v", got)
	}
	for _, id := range []string{"s1", "s2"} {
		if err := s.CreateSession(ctx, Session{IDHash: id, UserID: "u1", CSRF: "c-" + id, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.CreateSession(ctx, Session{IDHash: "old", UserID: "u1", CSRF: "c", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(-time.Second)})
	if got, err := s.GetSession(ctx, "s1"); err != nil || got.CSRF != "c-s1" || got.UserID != "u1" {
		t.Fatalf("session = %+v, %v", got, err)
	}
	_ = s.TouchSession(ctx, "s1", now.Add(time.Minute), now.Add(2*time.Hour))
	if got, _ := s.GetSession(ctx, "s1"); !got.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("touched = %+v", got)
	}
	if n, err := s.DeleteExpiredSessions(ctx, now); err != nil || n != 1 {
		t.Fatalf("expired deleted = %d, %v", n, err)
	}
	_ = s.DeleteUserSessions(ctx, "u1", "s1")
	if _, err := s.GetSession(ctx, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("other sessions must be gone")
	}
	_ = s.DeleteSession(ctx, "s1")
	if _, err := s.GetSession(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted session still there")
	}
}

// The database holds sealed secrets and session hashes: only its owner may read it.
func TestDatabaseFilesArePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghrm.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.PutMeta(context.Background(), "k", "v") // creates the WAL
	defer s.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(p), st.Mode().Perm())
		}
	}
}
