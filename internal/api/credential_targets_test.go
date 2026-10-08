package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/github"
)

func targetsHarness(t *testing.T, calls *int, fail bool) *harness {
	return newHarnessWith(t, "", func(d *Deps) {
		d.CredentialTargets = func(_ context.Context, token string) ([]github.Target, error) {
			*calls++
			if fail {
				return nil, errors.New("github: Resource not accessible by personal access token")
			}
			return []github.Target{
				{Kind: "organization", Owner: "acme", FullName: "acme", URL: "https://github.com/acme"},
				{Kind: "repository", Owner: "cocardoso", Name: "zeropaper", FullName: "cocardoso/zeropaper", URL: "https://github.com/cocardoso/zeropaper", Private: true},
			}, nil
		}
	})
}

func TestCredentialTargetsAreListedAndCached(t *testing.T) {
	calls := 0
	h := targetsHarness(t, &calls, false)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	for range 2 {
		code, b := h.callCode(t, "GET", "/api/v1/credentials/c/targets", nil, tok)
		if code != 200 || !strings.Contains(string(b), `"full_name":"cocardoso/zeropaper"`) || !strings.Contains(string(b), `"kind":"organization"`) {
			t.Fatalf("%d %s", code, b)
		}
	}
	if calls != 1 {
		t.Fatalf("GitHub called %d times; the list is cached for a while", calls)
	}
	if code, _ := h.callCode(t, "GET", "/api/v1/credentials/c/targets?refresh=true", nil, tok); code != 200 || calls != 2 {
		t.Fatalf("refresh: %d calls", calls)
	}
	if code, _ := h.callCode(t, "GET", "/api/v1/credentials/nope/targets", nil, tok); code != 404 {
		t.Fatalf("unknown credential = %d", code)
	}
}

func TestCredentialTargetsPassGitHubsErrorOn(t *testing.T) {
	calls := 0
	h := targetsHarness(t, &calls, true)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "GET", "/api/v1/credentials/c/targets", nil, tok)
	if code != 502 || !strings.Contains(string(b), "Resource not accessible") {
		t.Fatalf("%d %s", code, b)
	}
}

func TestATokenIsCheckedBeforeItIsSaved(t *testing.T) {
	calls := 0
	h := targetsHarness(t, &calls, false)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "POST", "/api/v1/credentials/check", map[string]any{"token": "github_pat_good"}, tok)
	if code != 200 || !strings.Contains(string(b), `"login":"octocat"`) || !strings.Contains(string(b), `"repositories":1`) || !strings.Contains(string(b), `"organizations":1`) {
		t.Fatalf("%d %s", code, b)
	}
	code, b = h.callCode(t, "POST", "/api/v1/credentials/check", map[string]any{"token": "nope"}, tok)
	if code != 200 || !strings.Contains(string(b), `"ok":false`) || !strings.Contains(string(b), "Bad credentials") {
		t.Fatalf("bad token: %d %s", code, b)
	}
}
