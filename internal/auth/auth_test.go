package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type harness struct {
	s   *Service
	db  *store.Store
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &harness{db: db, now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	h.s = &Service{Store: db, Now: func() time.Time { return h.now }, SetupToken: "setup-123", Params: TestParams}
	return h
}

func (h *harness) setup(t *testing.T) {
	t.Helper()
	if _, err := h.s.Setup(context.Background(), "setup-123", "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordHashIsArgon2idAndVerifies(t *testing.T) {
	enc, err := HashPassword("correct horse battery", DefaultParams)
	if err != nil || !strings.HasPrefix(enc, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash = %q, %v", enc, err)
	}
	if ok, err := VerifyPassword(enc, "correct horse battery"); !ok || err != nil {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if ok, _ := VerifyPassword(enc, "wrong"); ok {
		t.Fatal("a wrong password verified")
	}
	if again, _ := HashPassword("correct horse battery", DefaultParams); again == enc {
		t.Fatal("hashes must be salted")
	}
	if _, err := VerifyPassword("$bcrypt$nope", "x"); err == nil {
		t.Fatal("a malformed hash must be an error")
	}
}

func TestSetupNeedsTheSetupTokenAndRunsOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if need, _ := h.s.NeedsSetup(ctx); !need {
		t.Fatal("a fresh install needs setup")
	}
	if _, err := h.s.Setup(ctx, "guess", "admin", "correct horse battery"); !errors.Is(err, ErrBadSetupToken) {
		t.Fatalf("wrong setup token: %v", err)
	}
	if _, err := h.s.Setup(ctx, "setup-123", "admin", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password: %v", err)
	}
	if _, err := h.s.Setup(ctx, "setup-123", "", "correct horse battery"); err == nil {
		t.Fatal("an empty username must be refused")
	}
	u, err := h.s.Setup(ctx, "setup-123", "admin", "correct horse battery")
	if err != nil || u.Username != "admin" {
		t.Fatalf("setup = %+v, %v", u, err)
	}
	if _, err := h.s.Setup(ctx, "setup-123", "other", "correct horse battery"); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("second setup: %v", err)
	}
	if need, _ := h.s.NeedsSetup(ctx); need {
		t.Fatal("setup is done")
	}
}

func TestSetupTokenFileIsCreatedAndRemovedAfterSetup(t *testing.T) {
	dir := t.TempDir()
	tok, err := LoadOrCreateSetupToken(dir)
	if err != nil || len(tok) < 20 {
		t.Fatalf("token %q, %v", tok, err)
	}
	again, _ := LoadOrCreateSetupToken(dir)
	st, _ := os.Stat(filepath.Join(dir, "setup-token"))
	if again != tok || st.Mode().Perm() != 0o600 {
		t.Fatalf("token changed or mode %v", st.Mode())
	}
	h := newHarness(t)
	h.s.SetupToken, h.s.SetupTokenFile = tok, filepath.Join(dir, "setup-token")
	if _, err := h.s.Setup(context.Background(), tok, "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "setup-token")); !os.IsNotExist(err) {
		t.Fatal("the setup token file must be removed after setup")
	}
}

func TestLoginCreatesASessionAndLogoutEndsIt(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	sess, cookie, err := h.s.Login(ctx, "admin", "correct horse battery", "10.0.0.5", "test")
	if err != nil || cookie == "" || sess.CSRF == "" || !sess.ExpiresAt.Equal(h.now.Add(DefaultSessionTTL)) {
		t.Fatalf("login = %+v %q, %v", sess, cookie, err)
	}
	if raw, _ := h.db.GetSession(ctx, cookie); raw.IDHash != "" {
		t.Fatal("the store must hold the cookie's hash, not the cookie")
	}
	got, u, err := h.s.Authenticate(ctx, cookie)
	if err != nil || u.Username != "admin" || got.CSRF != sess.CSRF {
		t.Fatalf("authenticate = %+v %+v, %v", got, u, err)
	}
	if err := h.s.Logout(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Authenticate(ctx, cookie); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after logout: %v", err)
	}
	if _, _, err := h.s.Authenticate(ctx, ""); !errors.Is(err, ErrNoSession) {
		t.Fatalf("empty cookie: %v", err)
	}
}

func TestUnknownUserAndWrongPasswordLookAlike(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	_, _, e1 := h.s.Login(ctx, "nobody", "correct horse battery", "a", "")
	_, _, e2 := h.s.Login(ctx, "admin", "wrong password!", "b", "")
	if !errors.Is(e1, ErrBadCredentials) || !errors.Is(e2, ErrBadCredentials) || e1.Error() != e2.Error() {
		t.Fatalf("errors %v / %v; want the same ErrBadCredentials", e1, e2)
	}
}

func TestLoginThrottlesRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	for range 5 {
		if _, _, err := h.s.Login(ctx, "admin", "wrong password!", "10.0.0.9", ""); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("failure: %v", err)
		}
	}
	if _, _, err := h.s.Login(ctx, "admin", "correct horse battery", "10.0.0.9", ""); !errors.Is(err, ErrThrottled) {
		t.Fatalf("locked out = %v, want ErrThrottled even with the right password", err)
	}
	if _, _, err := h.s.Login(ctx, "admin", "correct horse battery", "10.0.0.10", ""); err != nil {
		t.Fatalf("another address is not locked: %v", err)
	}
	h.now = h.now.Add(2 * time.Minute)
	if _, _, err := h.s.Login(ctx, "admin", "correct horse battery", "10.0.0.9", ""); err != nil {
		t.Fatalf("after the lockout: %v", err)
	}
}

func TestSessionsExpireAndSlide(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	_, cookie, _ := h.s.Login(ctx, "admin", "correct horse battery", "a", "")
	h.now = h.now.Add(DefaultSessionTTL - time.Hour)
	if _, _, err := h.s.Authenticate(ctx, cookie); err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	h.now = h.now.Add(2 * time.Hour) // past the original expiry, within the slid one
	if _, _, err := h.s.Authenticate(ctx, cookie); err != nil {
		t.Fatalf("use slides the expiry: %v", err)
	}
	h.now = h.now.Add(DefaultSessionTTL + time.Minute)
	if _, _, err := h.s.Authenticate(ctx, cookie); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired: %v", err)
	}
}

func TestChangePasswordEndsOtherSessions(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	s1, c1, _ := h.s.Login(ctx, "admin", "correct horse battery", "a", "")
	_, c2, _ := h.s.Login(ctx, "admin", "correct horse battery", "b", "")
	if err := h.s.ChangePassword(ctx, s1, "wrong password!", "a new long password"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := h.s.ChangePassword(ctx, s1, "correct horse battery", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak: %v", err)
	}
	if err := h.s.ChangePassword(ctx, s1, "correct horse battery", "a new long password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Authenticate(ctx, c1); err != nil {
		t.Fatalf("the current session stays: %v", err)
	}
	if _, _, err := h.s.Authenticate(ctx, c2); !errors.Is(err, ErrNoSession) {
		t.Fatalf("other sessions end: %v", err)
	}
	if _, _, err := h.s.Login(ctx, "admin", "a new long password", "c", ""); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

// Each sign-in hashes with 64 MiB: concurrent attempts must not all hash at once (a
// burst would exhaust memory), and attempts in flight count toward the lockout.
func TestConcurrentSignInsAreBounded(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := map[error]int{}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := h.s.Login(ctx, "admin", "wrong password!", "10.0.0.66", "")
			mu.Lock()
			results[err]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if results[ErrBadCredentials] > maxFailures || results[ErrThrottled] < 20-maxFailures {
		t.Fatalf("results = %v; want at most %d checked, the rest throttled", results, maxFailures)
	}
	if peak := hashPeak(); peak > maxConcurrentHashes {
		t.Fatalf("peak concurrent hashes = %d, want at most %d", peak, maxConcurrentHashes)
	}
}

func TestConcurrentSetupsCreateOneAccount(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.s.Setup(ctx, "setup-123", fmt.Sprintf("admin%d", i), "correct horse battery")
		}()
	}
	wg.Wait()
	if n, _ := h.db.CountUsers(ctx); n != 1 {
		t.Fatalf("users = %d, want exactly one admin", n)
	}
}

func TestSlidingSessionsReportTheirNewExpiry(t *testing.T) {
	h := newHarness(t)
	h.setup(t)
	ctx := context.Background()
	_, cookie, _ := h.s.Login(ctx, "admin", "correct horse battery", "a", "")
	if s, _, _ := h.s.Authenticate(ctx, cookie); s.Refreshed {
		t.Fatal("a session used right away is not extended")
	}
	h.now = h.now.Add(2 * time.Minute)
	s, _, err := h.s.Authenticate(ctx, cookie)
	if err != nil || !s.Refreshed || !s.ExpiresAt.Equal(h.now.Add(DefaultSessionTTL)) {
		t.Fatalf("session = %+v, %v; want it extended and flagged, so the cookie is renewed", s, err)
	}
}
