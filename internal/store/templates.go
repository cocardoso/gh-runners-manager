package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Template version states (spec §8.5).
const (
	TemplateBuilding  = "building"  // the builder environment is running
	TemplateCreating  = "creating"  // the archive arrived; the template LXC is being created
	TemplateVerifying = "verifying" // the verify environment is running
	TemplateReady     = "ready"     // verified, not active
	TemplateActive    = "active"
	TemplateFailed    = "failed"
	TemplateRetired   = "retired" // beyond the retention count, deleted once unused
	TemplateDeleted   = "deleted"
)

// Environment kinds.
const (
	KindJob    = "job"
	KindBuild  = "build"
	KindVerify = "verify"
)

// Template is one template version.
type Template struct {
	ID            string
	SlimRelease   string
	RunnerVersion string
	LayerVersion  string
	State         string
	VMID          int
	RuntimeRef    string // runtime.TemplateRef; empty for the bootstrap template
	Volume        string
	ArchiveSHA256 string
	SizeBytes     int64
	Pinned        bool
	Trigger       string
	BuildEnvID    string
	VerifyEnvID   string
	FailureStage  string
	FailureReason string
	Report        []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ActivatedAt   time.Time
}

const tplColumns = `id, slim_release, runner_version, layer_version, state, vmid, runtime_ref, volume, archive_sha256, size_bytes, pinned,
	trigger, build_env_id, verify_env_id, failure_stage, failure_reason, report, created_at, updated_at, activated_at`

func scanTemplate(row scanner) (Template, error) {
	var t Template
	var pinned int
	var report string
	var created, updated, activated int64
	err := row.Scan(&t.ID, &t.SlimRelease, &t.RunnerVersion, &t.LayerVersion, &t.State, &t.VMID, &t.RuntimeRef, &t.Volume, &t.ArchiveSHA256,
		&t.SizeBytes, &pinned, &t.Trigger, &t.BuildEnvID, &t.VerifyEnvID, &t.FailureStage, &t.FailureReason, &report,
		&created, &updated, &activated)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, err
	}
	t.Pinned = pinned != 0
	if report != "" {
		t.Report = []byte(report)
	}
	t.CreatedAt, t.UpdatedAt = fromMs(created), fromMs(updated)
	if activated > 0 {
		t.ActivatedAt = fromMs(activated)
	}
	return t, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func activatedArg(t Template) int64 {
	if t.ActivatedAt.IsZero() {
		return 0
	}
	return ms(t.ActivatedAt)
}

// CreateTemplate inserts a template version.
func (s *Store) CreateTemplate(ctx context.Context, t Template) error {
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = t.CreatedAt
	_, err := s.db.ExecContext(ctx, `INSERT INTO templates (`+tplColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.SlimRelease, t.RunnerVersion, t.LayerVersion, t.State, t.VMID, t.RuntimeRef, t.Volume, t.ArchiveSHA256, t.SizeBytes, boolInt(t.Pinned),
		t.Trigger, t.BuildEnvID, t.VerifyEnvID, t.FailureStage, t.FailureReason, string(t.Report), ms(t.CreatedAt), ms(t.UpdatedAt), activatedArg(t))
	if err != nil {
		return fmt.Errorf("store: create template %s: %w", t.ID, err)
	}
	return nil
}

// GetTemplate returns one template version or ErrNotFound.
func (s *Store) GetTemplate(ctx context.Context, id string) (Template, error) {
	return scanTemplate(s.db.QueryRowContext(ctx, `SELECT `+tplColumns+` FROM templates WHERE id = ?`, id))
}

// ActiveTemplate returns the active version or ErrNotFound.
func (s *Store) ActiveTemplate(ctx context.Context) (Template, error) {
	return scanTemplate(s.db.QueryRowContext(ctx, `SELECT `+tplColumns+` FROM templates WHERE state = 'active'`))
}

// ListTemplates returns every version, newest first.
func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tplColumns+` FROM templates ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTemplate writes every field of t.
func (s *Store) UpdateTemplate(ctx context.Context, t Template) error {
	t.UpdatedAt = time.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE templates SET slim_release=?, runner_version=?, layer_version=?, state=?, vmid=?, runtime_ref=?, volume=?,
		archive_sha256=?, size_bytes=?, pinned=?, trigger=?, build_env_id=?, verify_env_id=?, failure_stage=?, failure_reason=?, report=?,
		updated_at=?, activated_at=? WHERE id=?`,
		t.SlimRelease, t.RunnerVersion, t.LayerVersion, t.State, t.VMID, t.RuntimeRef, t.Volume, t.ArchiveSHA256, t.SizeBytes, boolInt(t.Pinned),
		t.Trigger, t.BuildEnvID, t.VerifyEnvID, t.FailureStage, t.FailureReason, string(t.Report), ms(t.UpdatedAt), activatedArg(t), t.ID)
	if err != nil {
		return fmt.Errorf("store: update template %s: %w", t.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetActiveTemplate makes id the active version and the previous active one "ready".
func (s *Store) SetActiveTemplate(ctx context.Context, id string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := scanTemplate(tx.QueryRowContext(ctx, `SELECT `+tplColumns+` FROM templates WHERE id = ?`, id)); err != nil {
		return err
	}
	now := ms(time.Now())
	if _, err := tx.ExecContext(ctx, `UPDATE templates SET state='ready', updated_at=? WHERE state='active' AND id<>?`, now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE templates SET state='active', activated_at=?, updated_at=? WHERE id=?`, ms(at), now, id); err != nil {
		return err
	}
	return tx.Commit()
}
