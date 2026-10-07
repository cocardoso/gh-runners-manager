package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User is a UI account.
type User struct {
	ID                string
	Username          string
	PasswordHash      string
	CreatedAt         time.Time
	PasswordChangedAt time.Time
}

// Session is a signed-in browser. IDHash is the SHA-256 of the cookie value.
type Session struct {
	IDHash     string
	UserID     string
	CSRF       string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
	RemoteAddr string
}

// CreateUser adds an account; a taken username is ErrConflict.
func (s *Store) CreateUser(ctx context.Context, u User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, created_at, password_changed_at) VALUES (?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash, ms(u.CreatedAt), ms(u.PasswordChangedAt))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("%w: user %s exists", ErrConflict, u.Username)
	}
	return err
}

const userColumns = `id, username, password_hash, created_at, password_changed_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created, changed int64
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &created, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.CreatedAt, u.PasswordChangedAt = fromMs(created), fromMs(changed)
	return u, err
}

// GetUser returns an account by ID or ErrNotFound.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// GetUserByName returns an account by username or ErrNotFound.
func (s *Store) GetUserByName(ctx context.Context, username string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE username = ?`, username))
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdatePassword replaces an account's password hash.
func (s *Store) UpdatePassword(ctx context.Context, id, hash string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ? WHERE id = ?`, hash, ms(at), id)
	return err
}

// CreateSession records a session.
func (s *Store) CreateSession(ctx context.Context, x Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, csrf, created_at, last_seen_at, expires_at, user_agent, remote_addr)
		VALUES (?,?,?,?,?,?,?,?)`, x.IDHash, x.UserID, x.CSRF, ms(x.CreatedAt), ms(x.LastSeenAt), ms(x.ExpiresAt), x.UserAgent, x.RemoteAddr)
	return err
}

// GetSession returns a session by the hash of its cookie, or ErrNotFound.
func (s *Store) GetSession(ctx context.Context, idHash string) (Session, error) {
	var x Session
	var created, seen, expires int64
	err := s.db.QueryRowContext(ctx, `SELECT id_hash, user_id, csrf, created_at, last_seen_at, expires_at, user_agent, remote_addr
		FROM sessions WHERE id_hash = ?`, idHash).Scan(&x.IDHash, &x.UserID, &x.CSRF, &created, &seen, &expires, &x.UserAgent, &x.RemoteAddr)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	x.CreatedAt, x.LastSeenAt, x.ExpiresAt = fromMs(created), fromMs(seen), fromMs(expires)
	return x, err
}

// TouchSession records use of a session and extends it.
func (s *Store) TouchSession(ctx context.Context, idHash string, seen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`, ms(seen), ms(expires), idHash)
	return err
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteUserSessions ends every session of a user except one (empty: all).
func (s *Store) DeleteUserSessions(ctx context.Context, userID, except string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash != ?`, userID, except)
	return err
}

// DeleteExpiredSessions removes sessions that expired before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, ms(now))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
