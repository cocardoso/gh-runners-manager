package backup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func TestBackupWritesAUsableCopyAndKeepsN(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_ = db.PutMeta(ctx, "marker", "kept")
	out := filepath.Join(dir, "backups")
	b := &Backup{Store: db, Dir: out, Keep: 2}
	day := time.Date(2026, 10, 7, 3, 0, 0, 0, time.UTC)
	for i := range 3 {
		if _, err := b.Once(ctx, day.Add(time.Duration(i)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(out)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "ghrm-20261008-0300.db" || names[1] != "ghrm-20261009-0300.db" {
		t.Fatalf("backups = %v; want the two newest", names)
	}
	copyDB, err := store.Open(ctx, filepath.Join(out, names[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	if v, err := copyDB.GetMeta(ctx, "marker"); err != nil || v != "kept" {
		t.Fatalf("copy marker = %q, %v", v, err)
	}
}

func TestNextRunIsTheNextOccurrenceOfTheHour(t *testing.T) {
	loc := time.UTC
	at := func(h, m int) time.Time { return time.Date(2026, 10, 7, h, m, 0, 0, loc) }
	if got := nextRun(at(1, 0), 3); !got.Equal(at(3, 0)) {
		t.Fatalf("before the hour: %v", got)
	}
	if got := nextRun(at(3, 0), 3); !got.Equal(at(3, 0).Add(24 * time.Hour)) {
		t.Fatalf("at the hour: %v", got)
	}
	if got := nextRun(at(5, 0), 3); !got.Equal(at(3, 0).Add(24 * time.Hour)) {
		t.Fatalf("after the hour: %v", got)
	}
}

func TestBackupDirectoryIsPrivate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := filepath.Join(dir, "backups")
	_ = os.MkdirAll(out, 0o755) // an existing, readable directory
	if _, err := (&Backup{Store: db, Dir: out, Keep: 1}).Once(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir mode = %v, want 0700 (copies are written before they are chmod'ed)", st.Mode().Perm())
	}
}
