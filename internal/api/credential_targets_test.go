package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/github"
)

func targetsHarness(t *testing.T, calls *int, fail bool) *harness {
	return newHarnessWith(t, "", func(d *Deps) {
		d.CredentialTargets = func(_ context.Context, token string) (github.TargetList, error) {
			*calls++
			if fail {
				return github.TargetList{}, errors.New("github: Resource not accessible by personal access token")
			}
			return github.TargetList{Truncated: true, Targets: []github.Target{
				{Kind: "organization", Owner: "acme", FullName: "acme", URL: "https://github.com/acme"},
				{Kind: "repository", Owner: "octo", Name: "app", FullName: "octo/app", URL: "https://github.com/octo/app", Private: true},
			}}, nil
		}
	})
}

func TestCredentialTargetsAreListedAndCached(t *testing.T) {
	calls := 0
	h := targetsHarness(t, &calls, false)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	for range 2 {
		code, b := h.callCode(t, "GET", "/api/v1/credentials/c/targets", nil, tok)
		if code != 200 || !strings.Contains(string(b), `"full_name":"octo/app"`) || !strings.Contains(string(b), `"kind":"organization"`) || !strings.Contains(string(b), `"truncated":true`) {
			t.Fatalf("%d %s", code, b)
		}
	}
	if calls != 1 {
		t.Fatalf("GitHub called %d times; the list is cached for a while", calls)
	}
	// A refresh asks GitHub again, but not twice within a few seconds.
	if code, _ := h.callCode(t, "GET", "/api/v1/credentials/c/targets?refresh=true", nil, tok); code != 200 || calls != 1 {
		t.Fatalf("refresh right after a listing: %d calls", calls)
	}
	defer func(old time.Duration) { targetsRefreshMin = old }(targetsRefreshMin)
	targetsRefreshMin = 0
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
	if code != 502 || !strings.Contains(string(b), `"detail":"Resource not accessible`) {
		t.Fatalf("%d %s", code, b)
	}
}

func TestATokenIsCheckedBeforeItIsSaved(t *testing.T) {
	calls := 0
	h := targetsHarness(t, &calls, false)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "POST", "/api/v1/credentials/check", map[string]any{"token": "github_pat_good"}, tok)
	if code != 200 || !strings.Contains(string(b), `"login":"octocat"`) || !strings.Contains(string(b), `"repositories":1`) || !strings.Contains(string(b), `"organizations":1`) || !strings.Contains(string(b), `"truncated":true`) {
		t.Fatalf("%d %s", code, b)
	}
	code, b = h.callCode(t, "POST", "/api/v1/credentials/check", map[string]any{"token": "nope"}, tok)
	if code != 200 || !strings.Contains(string(b), `"ok":false`) || !strings.Contains(string(b), "Bad credentials") {
		t.Fatalf("bad token: %d %s", code, b)
	}
}

func TestATokenThatWorksButCannotListSaysWhy(t *testing.T) {
	calls := 0
	h := targetsHarness(t, &calls, true)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "POST", "/api/v1/credentials/check", map[string]any{"token": "github_pat_good"}, tok)
	if code != 200 || !strings.Contains(string(b), `"ok":true`) || !strings.Contains(string(b), `"error":"Resource not accessible`) {
		t.Fatalf("%d %s", code, b)
	}
}

func TestTokenChecksAreUnavailableWithoutGitHub(t *testing.T) {
	h := newHarnessWith(t, "", func(d *Deps) { d.TestCredential = nil })
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "POST", "/api/v1/credentials/check", map[string]any{"token": "x"}, tok)
	if code != 409 || !strings.Contains(string(b), "token checks are unavailable") {
		t.Fatalf("%d %s", code, b)
	}
}

func TestTargetsCacheForgetsReplacedTokensAndRemovedCredentials(t *testing.T) {
	c := &targetsCache{}
	c.put("a", "t1", github.TargetList{})
	if _, ok := c.get("a", "t2", targetsTTL); ok {
		t.Fatal("a replaced token is asked again")
	}
	if len(c.entries) != 0 {
		t.Fatal("the stale entry is dropped")
	}
	c.put("b", "t1", github.TargetList{})
	c.forget("b")
	if len(c.entries) != 0 {
		t.Fatal("a removed credential is forgotten")
	}
}

func TestTwoListingsAtOnceAskGitHubOnce(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	h := newHarnessWith(t, "", func(d *Deps) {
		d.CredentialTargets = func(context.Context, string) (github.TargetList, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			return github.TargetList{}, nil
		}
	})
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.callCode(t, "GET", "/api/v1/credentials/c/targets", nil, tok)
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("GitHub called %d times", calls)
	}
}
