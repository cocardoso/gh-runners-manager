package github

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/settings"
)

type fakeSource struct {
	sets  map[string]settings.ScaleSet
	creds map[string]settings.Credential
}

func (f *fakeSource) ScaleSet(name string) (settings.ScaleSet, bool) {
	s, ok := f.sets[name]
	return s, ok
}
func (f *fakeSource) Credential(name string) (settings.Credential, bool) {
	c, ok := f.creds[name]
	return c, ok
}

// Credentials and scale sets can change at runtime (the UI edits them): the client
// must use the current token, not the one it first saw.
func TestGitHubClientPicksUpANewToken(t *testing.T) {
	src := &fakeSource{
		sets:  map[string]settings.ScaleSet{"lab": {ScaleSet: config.ScaleSet{Name: "lab", URL: "https://github.com/o/r", Credential: "c"}}},
		creds: map[string]settings.Credential{"c": {Name: "c", Token: "t1"}},
	}
	c := New(src, slog.New(slog.DiscardHandler))
	a, _, err := c.client("lab")
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := c.client("lab")
	if a != b {
		t.Fatal("an unchanged credential must reuse the client")
	}
	src.creds["c"] = settings.Credential{Name: "c", Token: "t2"}
	if b, _, _ = c.client("lab"); a == b {
		t.Fatal("a new token must give a new client")
	}
	if _, _, err := c.client("nope"); err == nil {
		t.Fatal("unknown scale set")
	}
}

func TestUserReportsTheLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer srv.Close()
	r := &REST{BaseURL: srv.URL, HTTP: srv.Client()}
	if login, err := r.User(context.Background(), "good"); err != nil || login != "octocat" {
		t.Fatalf("login = %q, %v", login, err)
	}
	if _, err := r.User(context.Background(), "bad"); err == nil {
		t.Fatal("bad token must fail")
	}
}
