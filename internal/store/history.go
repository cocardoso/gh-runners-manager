package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HistoryCounts is how much history a cleanup deletes (or would delete).
type HistoryCounts struct {
	Environments int `json:"environments"`
	Jobs         int `json:"jobs"`
	Events       int `json:"events"`
	AuditEvents  int `json:"audit_events"`
	Templates    int `json:"templates"`
}

// Environments whose logs are the build or verification record of a template that is
// still kept (not failed or deleted).
const keptTemplateEnvs = `SELECT build_env_id FROM templates WHERE state NOT IN ('failed','deleted') AND build_env_id != ''
	UNION SELECT verify_env_id FROM templates WHERE state NOT IN ('failed','deleted') AND verify_env_id != ''`

const oldEnvs = `SELECT id FROM environments WHERE state = 'destroyed' AND state_changed_at < ?1 AND id NOT IN (` + keptTemplateEnvs + `)`

// A finished job goes with its environment; a job without one (or whose environment is
// already gone) goes by its own age.
const oldJobs = `SELECT id FROM jobs WHERE (finished_at != 0 AND finished_at < ?1 AND (environment_id = '' OR environment_id NOT IN (SELECT id FROM environments)))
	OR (environment_id != '' AND environment_id IN (` + oldEnvs + `))`

// Old events, except audit events and the timelines of work that is kept (environments
// not destroyed yet, and the build and verify environments of kept templates).
const oldEvents = `SELECT seq FROM events WHERE ts < ?1 AND kind NOT LIKE 'audit.%' AND (environment_id = ''
	OR (environment_id NOT IN (SELECT id FROM environments WHERE state != 'destroyed') AND environment_id NOT IN (` + keptTemplateEnvs + `)))`

// querier is *sql.DB or *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func countHistory(ctx context.Context, q querier, before, auditBefore int64) (HistoryCounts, error) {
	var c HistoryCounts
	for _, x := range []struct {
		dst  *int
		sql  string
		args []any
	}{
		{&c.Environments, `SELECT COUNT(*) FROM (` + oldEnvs + `)`, []any{before}},
		{&c.Jobs, `SELECT COUNT(*) FROM (` + oldJobs + `)`, []any{before}},
		{&c.Events, `SELECT COUNT(*) FROM (` + oldEvents + `)`, []any{before}},
		{&c.AuditEvents, `SELECT COUNT(*) FROM events WHERE ts < ? AND kind LIKE 'audit.%'`, []any{auditBefore}},
		{&c.Templates, `SELECT COUNT(*) FROM templates WHERE state IN ('failed','deleted') AND updated_at < ?`, []any{before}},
	} {
		if err := q.QueryRowContext(ctx, x.sql, x.args...).Scan(x.dst); err != nil {
			return HistoryCounts{}, fmt.Errorf("store: count history: %w", err)
		}
	}
	return c, nil
}

// CountHistory counts what DeleteHistory would delete.
func (s *Store) CountHistory(ctx context.Context, before, auditBefore time.Time) (HistoryCounts, error) {
	return countHistory(ctx, s.db, ms(before), ms(auditBefore))
}

// DeleteHistory deletes history older than before (audit events: auditBefore) and
// returns the counts and the ids of the deleted environments (their log files are the
// caller's to remove).
func (s *Store) DeleteHistory(ctx context.Context, before, auditBefore time.Time) (HistoryCounts, []string, error) {
	b, a := ms(before), ms(auditBefore)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HistoryCounts{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := countHistory(ctx, tx, b, a)
	if err != nil {
		return c, nil, err
	}
	rows, err := tx.QueryContext(ctx, oldEnvs, b)
	if err != nil {
		return c, nil, err
	}
	var envs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return c, nil, err
		}
		envs = append(envs, id)
	}
	rows.Close()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM jobs WHERE id IN (` + oldJobs + `)`, []any{b}},
		{`DELETE FROM events WHERE seq IN (` + oldEvents + `)`, []any{b}},
		{`DELETE FROM events WHERE ts < ? AND kind LIKE 'audit.%'`, []any{a}},
		{`DELETE FROM log_streams WHERE environment_id IN (` + oldEnvs + `)`, []any{b}},
		{`DELETE FROM environments WHERE id IN (` + oldEnvs + `)`, []any{b}},
		{`DELETE FROM templates WHERE state IN ('failed','deleted') AND updated_at < ?`, []any{b}},
	} {
		if _, err := tx.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			return c, nil, fmt.Errorf("store: delete history: %w", err)
		}
	}
	return c, envs, tx.Commit()
}

// DeleteEnvironmentHistory deletes one destroyed environment with its jobs, events and
// log streams. It is ErrConflict while the environment is not destroyed or its logs are
// a kept template's build or verification record.
func (s *Store) DeleteEnvironmentHistory(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM environments WHERE id = ?`, id).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "destroyed" {
		return fmt.Errorf("%w: environment %s is %s; only destroyed environments can be deleted", ErrConflict, id, state)
	}
	var kept int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+keptTemplateEnvs+`) WHERE build_env_id = ?`, id).Scan(&kept); err != nil {
		return err
	}
	if kept > 0 {
		return fmt.Errorf("%w: environment %s holds the build or verification logs of a kept template", ErrConflict, id)
	}
	for _, q := range []string{
		`DELETE FROM jobs WHERE environment_id = ?`,
		`DELETE FROM events WHERE environment_id = ? AND kind NOT LIKE 'audit.%'`, // who did what stays
		`DELETE FROM log_streams WHERE environment_id = ?`,
		`DELETE FROM environments WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteTemplateRecord deletes a failed or deleted template's record.
func (s *Store) DeleteTemplateRecord(ctx context.Context, id string) error {
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM templates WHERE id = ?`, id).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != TemplateFailed && state != TemplateDeleted {
		return fmt.Errorf("%w: template %s is %s; only failed or deleted records can be deleted", ErrConflict, id, state)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM templates WHERE id = ? AND state IN ('failed','deleted')`, id)
	return err
}

// Vacuum gives the space of deleted rows back to the file system.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return err
}
