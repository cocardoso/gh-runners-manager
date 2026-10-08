package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TemplateProfile is a stored template profile; Spec is its JSON (internal/template.Profile).
type TemplateProfile struct {
	Name      string
	Spec      []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

func scanProfile(row scanner) (TemplateProfile, error) {
	var p TemplateProfile
	var spec string
	var created, updated int64
	err := row.Scan(&p.Name, &spec, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateProfile{}, ErrNotFound
	}
	p.Spec, p.CreatedAt, p.UpdatedAt = []byte(spec), fromMs(created), fromMs(updated)
	return p, err
}

// ListTemplateProfiles returns the stored profiles by name.
func (s *Store) ListTemplateProfiles(ctx context.Context) ([]TemplateProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, spec, created_at, updated_at FROM template_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TemplateProfile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetTemplateProfile returns one profile or ErrNotFound.
func (s *Store) GetTemplateProfile(ctx context.Context, name string) (TemplateProfile, error) {
	return scanProfile(s.db.QueryRowContext(ctx, `SELECT name, spec, created_at, updated_at FROM template_profiles WHERE name = ?`, name))
}

// PutTemplateProfile creates or replaces a profile.
func (s *Store) PutTemplateProfile(ctx context.Context, name string, spec []byte) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO template_profiles (name, spec, created_at, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET spec = excluded.spec, updated_at = excluded.updated_at`, name, string(spec), now, now)
	if err != nil {
		return fmt.Errorf("store: put template profile %s: %w", name, err)
	}
	return nil
}

// DeleteTemplateProfile removes a profile; ErrNotFound when there is none.
func (s *Store) DeleteTemplateProfile(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM template_profiles WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("store: delete template profile %s: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
