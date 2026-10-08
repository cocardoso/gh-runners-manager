package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func openDB(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func TestServeAuthAnnouncesTheSetupTokenUntilSetup(t *testing.T) {
	db, dir := openDB(t)
	ctx := context.Background()
	var logs bytes.Buffer
	a, err := newAuth(ctx, db, dir, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(dir, "setup-token"))
	if a.SetupToken == "" || strings.TrimSpace(string(tok)) != a.SetupToken || !strings.Contains(logs.String(), "setup-token") {
		t.Fatalf("setup token %q, file %q, log %q", a.SetupToken, tok, logs.String())
	}
	if _, err := a.Setup(ctx, a.SetupToken, "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	a, _ = newAuth(ctx, db, dir, slog.New(slog.NewTextHandler(&logs, nil)))
	if a.SetupToken != "" || strings.Contains(logs.String(), "setup") {
		t.Fatalf("after setup: token %q, log %q", a.SetupToken, logs.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "setup-token")); !os.IsNotExist(err) {
		t.Fatal("no setup token file once an account exists")
	}
}

func TestDemoHasASignInAccount(t *testing.T) {
	db, _ := openDB(t)
	ctx := context.Background()
	a, err := demoAuth(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login(ctx, "admin", demoPassword, "127.0.0.1", ""); err != nil {
		t.Fatalf("demo sign-in: %v", err)
	}
	if _, err := demoAuth(ctx, db); err != nil {
		t.Fatalf("second start: %v", err)
	}
}

func TestDemoCacheLooksAlive(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	c := demoCache{start: start, now: func() time.Time { return start.Add(10 * time.Minute) }}
	s := c.Status()
	if !s.Enabled || !s.Up || len(s.Origins) != 4 || s.Origins[0].BlobHits <= s.Origins[0].BlobMisses || s.DiskUsed <= 0 || s.DiskUsed >= s.DiskBudget {
		t.Fatalf("demo cache = %+v", s)
	}
	later := demoCache{start: start, now: func() time.Time { return start.Add(20 * time.Minute) }}.Status()
	if later.Origins[0].BlobHits <= s.Origins[0].BlobHits {
		t.Fatal("hits grow over time")
	}
}
