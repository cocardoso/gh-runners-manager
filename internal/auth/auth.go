// Package auth owns the UI account, sessions and CSRF tokens (spec §10.6).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ids"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// DefaultSessionTTL is how long an unused session lasts; every use extends it.
const DefaultSessionTTL = 7 * 24 * time.Hour

// MinPasswordLength is the shortest accepted password.
const MinPasswordLength = 12

var (
	ErrSetupDone      = errors.New("auth: the admin account already exists")
	ErrBadSetupToken  = errors.New("auth: wrong setup token")
	ErrWeakPassword   = fmt.Errorf("auth: the password must have at least %d characters", MinPasswordLength)
	ErrBadUsername    = errors.New("auth: the username must be 1 to 64 letters, digits, '.', '_' or '-'")
	ErrBadCredentials = errors.New("auth: wrong username or password")
	ErrThrottled      = errors.New("auth: too many failed sign-ins from this address; try again later")
	ErrNoSession      = errors.New("auth: not signed in")
)

// User is the signed-in account.
type User struct {
	ID       string
	Username string
}

// Session is a signed-in browser. ID is the hash stored in the database.
type Session struct {
	ID        string
	UserID    string
	CSRF      string
	ExpiresAt time.Time
	// Refreshed means this use extended the session, so its cookie should be renewed.
	Refreshed bool
}

// Service implements setup, sign-in and sessions.
type Service struct {
	Store *store.Store
	Now   func() time.Time
	// SetupToken must be presented to create the first account; SetupTokenFile, when
	// set, is removed once that is done.
	SetupToken     string
	SetupTokenFile string
	SessionTTL     time.Duration
	Params         Params

	throttle  throttle
	dummyOnce sync.Once
	dummy     string // a hash verified for unknown users, so both failures take as long
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) ttl() time.Duration {
	if s.SessionTTL > 0 {
		return s.SessionTTL
	}
	return DefaultSessionTTL
}

func (s *Service) params() Params {
	if s.Params.Time == 0 {
		return DefaultParams
	}
	return s.Params
}

// NeedsSetup reports whether no account exists yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.Store.CountUsers(ctx)
	return n == 0, err
}

func validUsername(u string) bool {
	if u == "" || len(u) > 64 {
		return false
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Setup creates the admin account; it needs the setup token and works once.
func (s *Service) Setup(ctx context.Context, setupToken, username, password string) (User, error) {
	need, err := s.NeedsSetup(ctx)
	if err != nil {
		return User{}, err
	}
	if !need {
		return User{}, ErrSetupDone
	}
	if s.SetupToken == "" || subtle.ConstantTimeCompare([]byte(setupToken), []byte(s.SetupToken)) != 1 {
		return User{}, ErrBadSetupToken
	}
	if !validUsername(username) {
		return User{}, ErrBadUsername
	}
	if len([]rune(password)) < MinPasswordLength {
		return User{}, ErrWeakPassword
	}
	hash, err := HashPassword(password, s.params())
	if err != nil {
		return User{}, err
	}
	now := s.now()
	u := store.User{ID: ids.NewEnvironmentID(), Username: username, PasswordHash: hash, CreatedAt: now, PasswordChangedAt: now}
	if err := s.Store.CreateFirstUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return User{}, ErrSetupDone
		}
		return User{}, err
	}
	if s.SetupTokenFile != "" {
		_ = os.Remove(s.SetupTokenFile)
	}
	return User{ID: u.ID, Username: u.Username}, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashCookie(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

// Login checks the credentials and opens a session. It returns the session and the
// cookie value (only its hash is stored).
func (s *Service) Login(ctx context.Context, username, password, remoteAddr, userAgent string) (Session, string, error) {
	now := s.now()
	if !s.throttle.begin(remoteAddr, now) {
		return Session{}, "", ErrThrottled
	}
	defer s.throttle.end(remoteAddr)
	u, err := s.Store.GetUserByName(ctx, username)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.dummyOnce.Do(func() { s.dummy, _ = HashPassword("ghrm-dummy-password", s.params()) })
		_, _ = VerifyPassword(s.dummy, password)
		s.throttle.fail(remoteAddr, now)
		return Session{}, "", ErrBadCredentials
	case err != nil:
		return Session{}, "", err
	}
	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil {
		return Session{}, "", err
	}
	if !ok {
		s.throttle.fail(remoteAddr, now)
		return Session{}, "", ErrBadCredentials
	}
	s.throttle.succeed(remoteAddr)
	cookie, err := randomHex(32)
	if err != nil {
		return Session{}, "", err
	}
	csrf, err := randomHex(32)
	if err != nil {
		return Session{}, "", err
	}
	x := store.Session{IDHash: hashCookie(cookie), UserID: u.ID, CSRF: csrf, CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(s.ttl()), UserAgent: truncate(userAgent, 256), RemoteAddr: remoteAddr}
	if err := s.Store.CreateSession(ctx, x); err != nil {
		return Session{}, "", err
	}
	_, _ = s.Store.DeleteExpiredSessions(ctx, now)
	return Session{ID: x.IDHash, UserID: u.ID, CSRF: csrf, ExpiresAt: x.ExpiresAt}, cookie, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Authenticate resolves a cookie to its live session and user, extending the session
// (at most once a minute, to spare writes).
func (s *Service) Authenticate(ctx context.Context, cookie string) (Session, User, error) {
	if cookie == "" {
		return Session{}, User{}, ErrNoSession
	}
	x, err := s.Store.GetSession(ctx, hashCookie(cookie))
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, User{}, ErrNoSession
	}
	if err != nil {
		return Session{}, User{}, err
	}
	now := s.now()
	if !now.Before(x.ExpiresAt) {
		_ = s.Store.DeleteSession(ctx, x.IDHash)
		return Session{}, User{}, ErrNoSession
	}
	u, err := s.Store.GetUser(ctx, x.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, User{}, ErrNoSession
	}
	if err != nil {
		return Session{}, User{}, err
	}
	refreshed := false
	if now.Sub(x.LastSeenAt) >= time.Minute {
		x.ExpiresAt = now.Add(s.ttl())
		refreshed = s.Store.TouchSession(ctx, x.IDHash, now, x.ExpiresAt) == nil
	}
	return Session{ID: x.IDHash, UserID: x.UserID, CSRF: x.CSRF, ExpiresAt: x.ExpiresAt, Refreshed: refreshed}, User{ID: u.ID, Username: u.Username}, nil
}

// TimeLeft is how long a session lasts from now (for the cookie's Max-Age).
func (s *Service) TimeLeft(sess Session) time.Duration { return sess.ExpiresAt.Sub(s.now()) }

// Logout ends the session of a cookie.
func (s *Service) Logout(ctx context.Context, cookie string) error {
	if cookie == "" {
		return nil
	}
	return s.Store.DeleteSession(ctx, hashCookie(cookie))
}

// ChangePassword replaces the session user's password and ends their other sessions.
func (s *Service) ChangePassword(ctx context.Context, sess Session, current, next string) error {
	u, err := s.Store.GetUser(ctx, sess.UserID)
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(u.PasswordHash, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadCredentials
	}
	if len([]rune(next)) < MinPasswordLength {
		return ErrWeakPassword
	}
	hash, err := HashPassword(next, s.params())
	if err != nil {
		return err
	}
	if err := s.Store.UpdatePassword(ctx, u.ID, hash, s.now()); err != nil {
		return err
	}
	return s.Store.DeleteUserSessions(ctx, u.ID, sess.ID)
}

// LoadOrCreateSetupToken returns the one-time setup token in dataDir/setup-token,
// creating it (0600) when missing.
func LoadOrCreateSetupToken(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "setup-token")
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	tok, err := randomHex(16)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}
