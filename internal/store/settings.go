package store

import (
	"context"
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

// PutCredentialRecord creates or touches a UI credential.
func (s *Store) PutCredentialRecord(ctx context.Context, name, kind string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO credentials (name, kind, created_at, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, updated_at = excluded.updated_at`, name, kind, now, now)
	return err
}

// DeleteCredentialRecord removes a UI credential.
func (s *Store) DeleteCredentialRecord(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE name = ?`, name)
	return err
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
