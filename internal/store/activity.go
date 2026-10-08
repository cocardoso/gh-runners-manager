package store

import (
	"context"
	"fmt"
	"time"
)

// ScaleSetActivity is what a scale set's jobs did recently.
type ScaleSetActivity struct {
	ScaleSet string
	// Running counts the jobs assigned or running now.
	Running int
	// Jobs, Succeeded and Failed count the jobs completed since the "since" time.
	Jobs      int
	Succeeded int
	Failed    int
	// LastJob is the most recently updated job.
	LastJob Job
	// Repositories are the distinct repositories (owner/repo) of the jobs updated since
	// the "seenSince" time, sorted.
	Repositories []string
}

// ScaleSetActivity aggregates jobs by scale set: jobs completed since since, jobs now
// in progress, the last job and the repositories seen since seenSince. Scale sets without
// jobs are absent.
func (s *Store) ScaleSetActivity(ctx context.Context, since, seenSince time.Time) (map[string]ScaleSetActivity, error) {
	out := map[string]ScaleSetActivity{}
	rows, err := s.db.QueryContext(ctx, `SELECT scale_set,
			SUM(status IN ('assigned','running')),
			SUM(status = 'completed' AND finished_at >= ?1),
			SUM(status = 'completed' AND finished_at >= ?1 AND result = 'succeeded'),
			SUM(status = 'completed' AND finished_at >= ?1 AND result = 'failed')
		FROM jobs WHERE scale_set != '' GROUP BY scale_set`, ms(since))
	if err != nil {
		return nil, fmt.Errorf("store: scale set activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a ScaleSetActivity
		if err := rows.Scan(&a.ScaleSet, &a.Running, &a.Jobs, &a.Succeeded, &a.Failed); err != nil {
			return nil, err
		}
		out[a.ScaleSet] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	last, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY scale_set ORDER BY updated_at DESC, id DESC) AS rn
			FROM jobs WHERE scale_set != '') WHERE rn = 1`)
	if err != nil {
		return nil, fmt.Errorf("store: scale set last jobs: %w", err)
	}
	defer last.Close()
	for last.Next() {
		j, err := scanJob(last)
		if err != nil {
			return nil, err
		}
		a := out[j.ScaleSet]
		a.LastJob = j
		out[j.ScaleSet] = a
	}
	if err := last.Err(); err != nil {
		return nil, err
	}

	seen, err := s.db.QueryContext(ctx, `SELECT DISTINCT scale_set, repository FROM jobs
		WHERE scale_set != '' AND repository != '' AND updated_at >= ? ORDER BY scale_set, repository`, ms(seenSince))
	if err != nil {
		return nil, fmt.Errorf("store: scale set repositories: %w", err)
	}
	defer seen.Close()
	for seen.Next() {
		var ss, repo string
		if err := seen.Scan(&ss, &repo); err != nil {
			return nil, err
		}
		a := out[ss]
		a.Repositories = append(a.Repositories, repo)
		out[ss] = a
	}
	return out, seen.Err()
}
