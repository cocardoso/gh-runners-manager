package store

import (
	"context"
	"time"
)

// alertDismissalTTL is how long a dismissal is kept: alerts look back a day at most.
const alertDismissalTTL = 7 * 24 * time.Hour

// DismissAlerts records that the alerts with these IDs were dismissed at at.
func (s *Store) DismissAlerts(ctx context.Context, ids []string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_dismissals (id, dismissed_at) VALUES (?, ?)
			ON CONFLICT(id) DO UPDATE SET dismissed_at = excluded.dismissed_at`, id, ms(at)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_dismissals WHERE dismissed_at < ?`, ms(at.Add(-alertDismissalTTL))); err != nil {
		return err
	}
	return tx.Commit()
}

// AlertDismissals returns when each dismissed alert was dismissed.
func (s *Store) AlertDismissals(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, dismissed_at FROM alert_dismissals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = fromMs(at)
	}
	return out, rows.Err()
}
