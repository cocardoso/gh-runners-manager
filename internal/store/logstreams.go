package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// LogStream is the metadata of one raw log stream file.
type LogStream struct {
	EnvironmentID string    `json:"environment_id"`
	Stream        string    `json:"stream"`
	Path          string    `json:"-"`
	LastSeq       int64     `json:"last_seq"`
	Bytes         int64     `json:"bytes"`
	Lines         int64     `json:"lines"`
	FirstAt       time.Time `json:"first_at"`
	LastAt        time.Time `json:"last_at"`
}

func scanLogStream(row scanner) (LogStream, error) {
	var l LogStream
	var first, last int64
	err := row.Scan(&l.EnvironmentID, &l.Stream, &l.Path, &l.LastSeq, &l.Bytes, &l.Lines, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return LogStream{}, ErrNotFound
	}
	l.FirstAt, l.LastAt = fromMs(first), fromMs(last)
	return l, err
}

// GetLogStream returns one stream or ErrNotFound.
func (s *Store) GetLogStream(ctx context.Context, envID, stream string) (LogStream, error) {
	return scanLogStream(s.db.QueryRowContext(ctx, `SELECT environment_id, stream, path, last_seq, bytes, lines, first_at, last_at
		FROM log_streams WHERE environment_id = ? AND stream = ?`, envID, stream))
}

// UpsertLogStream inserts or replaces a stream's metadata.
func (s *Store) UpsertLogStream(ctx context.Context, l LogStream) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO log_streams (environment_id, stream, path, last_seq, bytes, lines, first_at, last_at)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(environment_id, stream) DO UPDATE SET
		path=excluded.path, last_seq=excluded.last_seq, bytes=excluded.bytes, lines=excluded.lines,
		first_at=excluded.first_at, last_at=excluded.last_at`,
		l.EnvironmentID, l.Stream, l.Path, l.LastSeq, l.Bytes, l.Lines, ms(l.FirstAt), ms(l.LastAt))
	return err
}

// ListLogStreams returns the streams of one environment.
func (s *Store) ListLogStreams(ctx context.Context, envID string) ([]LogStream, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT environment_id, stream, path, last_seq, bytes, lines, first_at, last_at
		FROM log_streams WHERE environment_id = ? ORDER BY stream`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogStream
	for rows.Next() {
		l, err := scanLogStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteLogStreams removes the stream records of an environment.
func (s *Store) DeleteLogStreams(ctx context.Context, envID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM log_streams WHERE environment_id = ?`, envID)
	return err
}
