package store

import (
	"context"
	"database/sql"
	"time"
)

// CredentialRecord is a credential created in the UI; its token lives in the vault.
type CredentialRecord struct {
	Name      string
	Kind      string // "github-pat"
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ScaleSetConfigRecord is a scale set created in the UI; Spec is its JSON settings.
type ScaleSetConfigRecord struct {
	Name      string
	Spec      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListCredentialRecords returns the UI credentials by name.
func (s *Store) ListCredentialRecords(ctx context.Context) ([]CredentialRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, kind, created_at, updated_at FROM credentials ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialRecord
	for rows.Next() {
		var r CredentialRecord
		var c, u int64
		if err := rows.Scan(&r.Name, &r.Kind, &c, &u); err != nil {
			return nil, err
		}
		r.CreatedAt, r.UpdatedAt = fromMs(c), fromMs(u)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutScaleSetConfig creates or replaces a UI scale set.
func (s *Store) PutScaleSetConfig(ctx context.Context, name, spec string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO scale_set_configs (name, spec, created_at, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET spec = excluded.spec, updated_at = excluded.updated_at`, name, spec, now, now)
	return err
}

// DeleteScaleSetConfig removes a UI scale set.
func (s *Store) DeleteScaleSetConfig(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scale_set_configs WHERE name = ?`, name)
	return err
}

// ListScaleSetConfigs returns the UI scale sets by name.
func (s *Store) ListScaleSetConfigs(ctx context.Context) ([]ScaleSetConfigRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, spec, created_at, updated_at FROM scale_set_configs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScaleSetConfigRecord
	for rows.Next() {
		var r ScaleSetConfigRecord
		var c, u int64
		if err := rows.Scan(&r.Name, &r.Spec, &c, &u); err != nil {
			return nil, err
		}
		r.CreatedAt, r.UpdatedAt = fromMs(c), fromMs(u)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutCredentialWithSecret records a UI credential and its sealed token in one transaction.
func (s *Store) PutCredentialWithSecret(ctx context.Context, name, kind, secretName string, sealed []byte) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO secrets (name, sealed, updated_at) VALUES (?,?,?)
			ON CONFLICT(name) DO UPDATE SET sealed = excluded.sealed, updated_at = excluded.updated_at`, secretName, sealed, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO credentials (name, kind, created_at, updated_at) VALUES (?,?,?,?)
			ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, updated_at = excluded.updated_at`, name, kind, now, now)
		return err
	})
}

// DeleteCredentialWithSecret removes a UI credential and its token in one transaction.
func (s *Store) DeleteCredentialWithSecret(ctx context.Context, name, secretName string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, secretName); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM credentials WHERE name = ?`, name)
		return err
	})
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
