package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// call sends a request exactly as written (no harness token).
func (h *harness) call(t *testing.T, method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, r)
	req.Header.Set("X-Test-No-Auth", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

type signedIn struct {
	cookie string
	csrf   string
}

func (h *harness) signIn(t *testing.T) signedIn {
	t.Helper()
	if resp, b := h.call(t, "POST", "/api/v1/auth/setup", map[string]string{"setup_token": "setup-tok", "username": "admin", "password": "correct horse battery"}, nil); resp.StatusCode != 200 {
		t.Fatalf("setup = %d %s", resp.StatusCode, b)
	}
	resp, b := h.call(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, b)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	_ = json.Unmarshal(b, &out)
	for _, c := range resp.Cookies() {
		if c.Name == "ghrm_session" {
			return signedIn{cookie: c.Name + "=" + c.Value, csrf: out.CSRF}
		}
	}
	t.Fatal("no session cookie")
	return signedIn{}
}

func TestAPIRequiresSignIn(t *testing.T) {
	h := newHarness(t, "")
	if resp, _ := h.call(t, "GET", "/api/v1/environments", nil, nil); resp.StatusCode != 401 {
		t.Fatalf("anonymous = %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.call(t, "GET", "/api/v1/environments", nil, map[string]string{"Authorization": "Bearer wrong"}); resp.StatusCode != 401 {
		t.Fatalf("wrong token = %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.call(t, "GET", "/api/v1/environments", nil, map[string]string{"Authorization": "Bearer " + harnessToken}); resp.StatusCode != 200 {
		t.Fatalf("token = %d", resp.StatusCode)
	}
	s := h.signIn(t)
	if resp, _ := h.call(t, "GET", "/api/v1/environments", nil, map[string]string{"Cookie": s.cookie}); resp.StatusCode != 200 {
		t.Fatalf("session = %d", resp.StatusCode)
	}
	for _, p := range []string{"/healthz", "/api/openapi.json", "/api/v1/auth/session"} {
		if resp, _ := h.call(t, "GET", p, nil, nil); resp.StatusCode != 200 {
			t.Errorf("%s = %d, want public", p, resp.StatusCode)
		}
	}
}

func TestSSERequiresSignIn(t *testing.T) {
	h := newHarness(t, "")
	_ = h.db.CreateEnvironment(context.Background(), store.Environment{ID: "env1", ScaleSet: "lab", State: "running"})
	for _, p := range []string{"/api/v1/events/stream", "/api/v1/environments/env1/logs/runtime?follow=true"} {
		if resp, _ := h.call(t, "GET", p, nil, nil); resp.StatusCode != 401 {
			t.Errorf("%s anonymous = %d, want 401", p, resp.StatusCode)
		}
	}
}

func TestCSRFRequiredForCookieWrites(t *testing.T) {
	h := newHarness(t, "")
	_ = h.db.CreateEnvironment(context.Background(), store.Environment{ID: "env1", ScaleSet: "lab", State: "running"})
	s := h.signIn(t)
	if resp, _ := h.call(t, "POST", "/api/v1/environments/env1/destroy", nil, map[string]string{"Cookie": s.cookie}); resp.StatusCode != 403 {
		t.Fatalf("no CSRF header = %d, want 403", resp.StatusCode)
	}
	if resp, _ := h.call(t, "POST", "/api/v1/environments/env1/destroy", nil, map[string]string{"Cookie": s.cookie, "X-CSRF-Token": "forged"}); resp.StatusCode != 403 {
		t.Fatalf("wrong CSRF = %d, want 403", resp.StatusCode)
	}
	if resp, _ := h.call(t, "POST", "/api/v1/environments/env1/destroy", nil, map[string]string{"Cookie": s.cookie, "X-CSRF-Token": s.csrf}); resp.StatusCode != 202 {
		t.Fatalf("with CSRF = %d, want 202", resp.StatusCode)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{EnvironmentID: "env1"})
	if evs[len(evs)-1].Data["actor"] != "admin" {
		t.Fatalf("audit actor = %v, want the signed-in user", evs[len(evs)-1].Data)
	}
}

func TestBearerTokenNeedsNoCSRF(t *testing.T) {
	h := newHarness(t, "s3cret")
	_ = h.db.CreateEnvironment(context.Background(), store.Environment{ID: "env1", ScaleSet: "lab", State: "running"})
	if resp, _ := h.call(t, "POST", "/api/v1/environments/env1/destroy", nil, map[string]string{"Authorization": "Bearer s3cret"}); resp.StatusCode != 202 {
		t.Fatalf("token write = %d", resp.StatusCode)
	}
}

func TestSessionCookieSecureOnlyOverHTTPS(t *testing.T) {
	h := newHarness(t, "")
	_, _ = h.call(t, "POST", "/api/v1/auth/setup", map[string]string{"setup_token": "setup-tok", "username": "admin", "password": "correct horse battery"}, nil)
	login := map[string]string{"username": "admin", "password": "correct horse battery"}
	resp, _ := h.call(t, "POST", "/api/v1/auth/login", login, nil)
	c := resp.Cookies()[0]
	if c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("plain HTTP cookie = %+v; want HttpOnly, SameSite=Strict, not Secure", c)
	}
	resp, _ = h.call(t, "POST", "/api/v1/auth/login", login, map[string]string{"X-Forwarded-Proto": "https"})
	if c := resp.Cookies()[0]; !c.Secure {
		t.Fatalf("behind an HTTPS proxy the cookie must be Secure: %+v", c)
	}
}

func TestSetupLoginLogoutFlow(t *testing.T) {
	h := newHarness(t, "")
	_, b := h.call(t, "GET", "/api/v1/auth/session", nil, nil)
	if !strings.Contains(string(b), `"state":"setup"`) {
		t.Fatalf("fresh install session = %s", b)
	}
	if resp, _ := h.call(t, "POST", "/api/v1/auth/setup", map[string]string{"setup_token": "nope", "username": "admin", "password": "correct horse battery"}, nil); resp.StatusCode != 403 {
		t.Fatalf("bad setup token = %d, want 403", resp.StatusCode)
	}
	if resp, b := h.call(t, "POST", "/api/v1/auth/setup", map[string]string{"setup_token": "setup-tok", "username": "admin", "password": "short"}, nil); resp.StatusCode != 422 {
		t.Fatalf("weak password = %d %s, want 422", resp.StatusCode, b)
	}
	s := h.signIn(t)
	if resp, _ := h.call(t, "POST", "/api/v1/auth/setup", map[string]string{"setup_token": "setup-tok", "username": "x", "password": "correct horse battery"}, nil); resp.StatusCode != 409 {
		t.Fatalf("second setup = %d, want 409", resp.StatusCode)
	}
	_, b = h.call(t, "GET", "/api/v1/auth/session", nil, nil)
	if !strings.Contains(string(b), `"state":"signed_out"`) {
		t.Fatalf("anonymous session = %s", b)
	}
	_, b = h.call(t, "GET", "/api/v1/auth/session", nil, map[string]string{"Cookie": s.cookie})
	if !strings.Contains(string(b), `"state":"signed_in"`) || !strings.Contains(string(b), `"username":"admin"`) || !strings.Contains(string(b), s.csrf) {
		t.Fatalf("signed-in session = %s", b)
	}
	if resp, _ := h.call(t, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong password!"}, nil); resp.StatusCode != 401 {
		t.Fatalf("wrong password = %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.call(t, "POST", "/api/v1/auth/password", map[string]string{"current": "correct horse battery", "next": "another long password"},
		map[string]string{"Cookie": s.cookie, "X-CSRF-Token": s.csrf}); resp.StatusCode != 204 {
		t.Fatalf("change password = %d", resp.StatusCode)
	}
	if resp, _ := h.call(t, "POST", "/api/v1/auth/logout", nil, map[string]string{"Cookie": s.cookie, "X-CSRF-Token": s.csrf}); resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	if resp, _ := h.call(t, "GET", "/api/v1/environments", nil, map[string]string{"Cookie": s.cookie}); resp.StatusCode != 401 {
		t.Fatalf("after logout = %d, want 401", resp.StatusCode)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	kinds := map[string]bool{}
	for _, e := range evs {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"audit.setup", "audit.login", "audit.login_failed", "audit.password", "audit.logout"} {
		if !kinds[k] {
			t.Errorf("missing %s in %v", k, kinds)
		}
	}
}

func TestMetricsEndpointIsPublic(t *testing.T) {
	h := newHarnessWith(t, "", func(d *Deps) {
		d.Metrics = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ghrm_build_info 1\n")) })
	})
	resp, b := h.call(t, "GET", "/metrics", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "ghrm_build_info") {
		t.Fatalf("metrics = %d %s", resp.StatusCode, b)
	}
}

// The server slides a session's expiry on use; the cookie must follow, or an active
// user is signed out a week after signing in.
func TestSlidingSessionRenewsTheCookie(t *testing.T) {
	h := newHarness(t, "")
	now := time.Now()
	h.auth.Now = func() time.Time { return now }
	s := h.signIn(t)
	resp, _ := h.call(t, "GET", "/api/v1/environments", nil, map[string]string{"Cookie": s.cookie})
	if len(resp.Cookies()) != 0 {
		t.Fatal("a session used right away needs no new cookie")
	}
	now = now.Add(2 * time.Minute)
	resp, _ = h.call(t, "GET", "/api/v1/environments", nil, map[string]string{"Cookie": s.cookie})
	cs := resp.Cookies()
	if len(cs) != 1 || cs[0].Name != SessionCookie || cs[0].MaxAge < int((7*24*time.Hour-time.Minute).Seconds()) || !cs[0].HttpOnly {
		t.Fatalf("cookies = %+v; want the session cookie renewed for the full lifetime", cs)
	}
}
