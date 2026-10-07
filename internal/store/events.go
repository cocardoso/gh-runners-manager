package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Event is one entry of the append-only timeline.
type Event struct {
	Seq           int64          `json:"seq"`
	Time          time.Time      `json:"time"`
	Kind          string         `json:"kind"`
	Level         string         `json:"level"`
	Message       string         `json:"message"`
	ScaleSet      string         `json:"scale_set,omitempty"`
	EnvironmentID string         `json:"environment_id,omitempty"`
	JobID         string         `json:"job_id,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
}

// EventFilter narrows ListEvents.
type EventFilter struct {
	AfterSeq int64
	// BeforeSeq, when positive, keeps only events with a lower sequence number.
	BeforeSeq int64
	// Newest returns the last Limit matching events instead of the first (still in ascending order).
	Newest        bool
	EnvironmentID string
	JobID         string
	Limit         int
}

// AppendEvent stores e and returns it with its sequence number.
func (s *Store) AppendEvent(ctx context.Context, e Event) (Event, error) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	data := []byte("{}")
	if len(e.Data) > 0 {
		var err error
		if data, err = json.Marshal(e.Data); err != nil {
			return Event{}, fmt.Errorf("store: event data: %w", err)
		}
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO events (ts, kind, level, message, scale_set, environment_id, job_id, data)
		VALUES (?,?,?,?,?,?,?,?)`, ms(e.Time), e.Kind, e.Level, e.Message, e.ScaleSet, e.EnvironmentID, e.JobID, string(data))
	if err != nil {
		return Event{}, fmt.Errorf("store: append event: %w", err)
	}
	e.Seq, err = res.LastInsertId()
	e.Time = fromMs(ms(e.Time))
	return e, err
}

// ListEvents returns events in ascending sequence order.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	where := []string{"seq > ?"}
	args := []any{f.AfterSeq}
	if f.BeforeSeq > 0 {
		where = append(where, "seq < ?")
		args = append(args, f.BeforeSeq)
	}
	if f.EnvironmentID != "" {
		where = append(where, "environment_id = ?")
		args = append(args, f.EnvironmentID)
	}
	if f.JobID != "" {
		where = append(where, "job_id = ?")
		args = append(args, f.JobID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	order := "ASC"
	if f.Newest {
		order = "DESC"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, ts, kind, level, message, scale_set, environment_id, job_id, data
		FROM events WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(" ORDER BY seq %s LIMIT %d", order, limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		var data string
		if err := rows.Scan(&e.Seq, &ts, &e.Kind, &e.Level, &e.Message, &e.ScaleSet, &e.EnvironmentID, &e.JobID, &data); err != nil {
			return nil, err
		}
		e.Time = fromMs(ts)
		if data != "" && data != "{}" {
			_ = json.Unmarshal([]byte(data), &e.Data)
		}
		out = append(out, e)
	}
	if f.Newest {
		slices.Reverse(out)
	}
	return out, rows.Err()
}

// LatestEventSeq returns the highest sequence number, or 0.
func (s *Store) LatestEventSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&seq)
	return seq, err
}
