package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ScaleSetRecord remembers the GitHub scale set behind a configured name.
type ScaleSetRecord struct {
	Name      string
	GitHubID  int
	URL       string
	UpdatedAt time.Time
}

// GetScaleSet returns a record or ErrNotFound.
func (s *Store) GetScaleSet(ctx context.Context, name string) (ScaleSetRecord, error) {
	var r ScaleSetRecord
	var upd int64
	err := s.db.QueryRowContext(ctx, `SELECT name, github_id, url, updated_at FROM scale_sets WHERE name = ?`, name).
		Scan(&r.Name, &r.GitHubID, &r.URL, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return ScaleSetRecord{}, ErrNotFound
	}
	r.UpdatedAt = fromMs(upd)
	return r, err
}

// PutScaleSet inserts or replaces a record.
func (s *Store) PutScaleSet(ctx context.Context, r ScaleSetRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO scale_sets (name, github_id, url, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET github_id=excluded.github_id, url=excluded.url, updated_at=excluded.updated_at`,
		r.Name, r.GitHubID, r.URL, time.Now().UnixMilli())
	return err
}
