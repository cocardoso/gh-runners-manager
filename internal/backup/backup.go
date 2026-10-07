// Package backup copies the database once a day with VACUUM INTO (spec §12.3).
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Backup writes dated copies of the database to Dir and keeps the newest Keep.
type Backup struct {
	Store *store.Store
	Dir   string
	Keep  int
	// Hour is the local hour (0-23) of the daily copy.
	Hour int
	// Done, when set, is told the outcome of each run.
	Done func(path string, err error)
}

// Once writes one copy named after now and prunes the old ones.
func (b *Backup) Once(ctx context.Context, now time.Time) (string, error) {
	// Owner only: VACUUM INTO creates the copy with the default umask before its chmod.
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(b.Dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(b.Dir, "ghrm-"+now.Format("20060102-1504")+".db")
	_ = os.Remove(path) // VACUUM INTO refuses an existing file (a rerun within the minute)
	if err := b.Store.BackupTo(ctx, path); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return path, err
	}
	return path, b.prune()
}

func (b *Backup) prune() error {
	entries, err := os.ReadDir(b.Dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "ghrm-") && strings.HasSuffix(n, ".db") {
			names = append(names, n)
		}
	}
	sort.Strings(names) // dated names sort by time
	keep := max(b.Keep, 1)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(b.Dir, names[0])); err != nil {
			return fmt.Errorf("backup: prune: %w", err)
		}
		names = names[1:]
	}
	return nil
}

// nextRun is the next time at hour (local to now), strictly after now.
func nextRun(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Run makes a copy every day at Hour until ctx ends.
func (b *Backup) Run(ctx context.Context) {
	for {
		wait := time.Until(nextRun(time.Now(), b.Hour))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		path, err := b.Once(ctx, time.Now())
		if b.Done != nil {
			b.Done(path, err)
		}
	}
}
