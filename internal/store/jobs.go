package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Job is one GitHub Actions job seen through a scale set.
type Job struct {
	ID            string
	ScaleSet      string
	Repository    string
	Owner         string
	WorkflowRef   string
	DisplayName   string
	EventName     string
	RunID         int64
	RunnerName    string
	EnvironmentID string
	Status        string // assigned, running, completed
	Result        string
	QueuedAt      time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	UpdatedAt     time.Time
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	ScaleSet string
	Status   string
	Limit    int
}

const jobColumns = `id, scale_set, repository, owner, workflow_ref, display_name, event_name, run_id, runner_name,
	environment_id, status, result, queued_at, started_at, finished_at, updated_at`

func scanJob(row scanner) (Job, error) {
	var j Job
	var q, st, fin, upd int64
	err := row.Scan(&j.ID, &j.ScaleSet, &j.Repository, &j.Owner, &j.WorkflowRef, &j.DisplayName, &j.EventName, &j.RunID,
		&j.RunnerName, &j.EnvironmentID, &j.Status, &j.Result, &q, &st, &fin, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	j.QueuedAt, j.StartedAt, j.FinishedAt, j.UpdatedAt = fromMs(q), fromMs(st), fromMs(fin), fromMs(upd)
	return j, err
}

// UpsertJob inserts a job or merges the non-zero fields of j into the existing row.
// UpsertJob creates or updates a job; empty fields keep their stored values, and the
// first queue time recorded is kept.
func (s *Store) UpsertJob(ctx context.Context, j Job) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs (`+jobColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			scale_set      = CASE WHEN excluded.scale_set != ''      THEN excluded.scale_set      ELSE jobs.scale_set END,
			repository     = CASE WHEN excluded.repository != ''     THEN excluded.repository     ELSE jobs.repository END,
			owner          = CASE WHEN excluded.owner != ''          THEN excluded.owner          ELSE jobs.owner END,
			workflow_ref   = CASE WHEN excluded.workflow_ref != ''   THEN excluded.workflow_ref   ELSE jobs.workflow_ref END,
			display_name   = CASE WHEN excluded.display_name != ''   THEN excluded.display_name   ELSE jobs.display_name END,
			event_name     = CASE WHEN excluded.event_name != ''     THEN excluded.event_name     ELSE jobs.event_name END,
			run_id         = CASE WHEN excluded.run_id != 0          THEN excluded.run_id         ELSE jobs.run_id END,
			runner_name    = CASE WHEN excluded.runner_name != ''    THEN excluded.runner_name    ELSE jobs.runner_name END,
			environment_id = CASE WHEN excluded.environment_id != '' THEN excluded.environment_id ELSE jobs.environment_id END,
			status         = CASE WHEN excluded.status != ''         THEN excluded.status         ELSE jobs.status END,
			result         = CASE WHEN excluded.result != ''         THEN excluded.result         ELSE jobs.result END,
			queued_at      = CASE WHEN jobs.queued_at = 0            THEN excluded.queued_at      ELSE jobs.queued_at END,
			started_at     = CASE WHEN excluded.started_at != 0      THEN excluded.started_at     ELSE jobs.started_at END,
			finished_at    = CASE WHEN excluded.finished_at != 0     THEN excluded.finished_at    ELSE jobs.finished_at END,
			updated_at     = excluded.updated_at`,
		j.ID, j.ScaleSet, j.Repository, j.Owner, j.WorkflowRef, j.DisplayName, j.EventName, j.RunID, j.RunnerName,
		j.EnvironmentID, j.Status, j.Result, ms(j.QueuedAt), ms(j.StartedAt), ms(j.FinishedAt), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("store: upsert job %s: %w", j.ID, err)
	}
	return nil
}

// GetJob returns one job or ErrNotFound.
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
}

// ListJobs returns jobs, most recently updated first.
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	var where []string
	var args []any
	if f.ScaleSet != "" {
		where = append(where, "scale_set = ?")
		args = append(args, f.ScaleSet)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	q := `SELECT ` + jobColumns + ` FROM jobs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated_at DESC, id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
