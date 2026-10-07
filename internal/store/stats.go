package store

import (
	"context"
	"fmt"
)

// Count is one row of a grouped count.
type Count struct {
	ScaleSet string
	Key      string
	N        int
}

func (s *Store) counts(ctx context.Context, query string) ([]Count, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.ScaleSet, &c.Key, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountEnvironmentsByState counts the environments that are not destroyed, by scale set and state.
func (s *Store) CountEnvironmentsByState(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT scale_set, state, COUNT(*) FROM environments WHERE state != 'destroyed' GROUP BY scale_set, state`)
}

// CountFailuresByStage counts the environments that failed, by failure stage.
func (s *Store) CountFailuresByStage(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT '', failure_stage, COUNT(*) FROM environments WHERE failure_stage != '' GROUP BY failure_stage`)
}

// CountCompletedJobs counts completed jobs by scale set and result.
func (s *Store) CountCompletedJobs(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT scale_set, result, COUNT(*) FROM jobs WHERE status = 'completed' GROUP BY scale_set, result`)
}

// CountTemplatesByState counts template versions by state.
func (s *Store) CountTemplatesByState(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT '', state, COUNT(*) FROM templates GROUP BY state`)
}

// BackupTo writes a consistent copy of the database to path (which must not exist).
func (s *Store) BackupTo(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("store: backup to %s: %w", path, err)
	}
	return nil
}
