package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PutSecret stores a sealed value (internal/secrets seals it).
func (s *Store) PutSecret(ctx context.Context, name string, sealed []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO secrets (name, sealed, updated_at) VALUES (?,?,?)
		ON CONFLICT(name) DO UPDATE SET sealed = excluded.sealed, updated_at = excluded.updated_at`,
		name, sealed, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("store: put secret %s: %w", name, err)
	}
	return nil
}

// GetSecret returns a sealed value or ErrNotFound.
func (s *Store) GetSecret(ctx context.Context, name string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT sealed FROM secrets WHERE name = ?`, name).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// DeleteSecret removes a value; a missing one is not an error.
func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name)
	return err
}

// ListSecretNames returns the stored names, sorted.
func (s *Store) ListSecretNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetMeta returns a control-plane setting or ErrNotFound.
func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// PutMeta sets a control-plane setting.
func (s *Store) PutMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
