package template

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func releasesServer(t *testing.T, runnerBody string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/actions/runner/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v2.338.0","body":` + runnerBody + `}`))
		case "/repos/actions/runner-images/releases":
			_, _ = w.Write([]byte(`[
				{"tag_name":"ubuntu24/20261006.1","draft":false,"prerelease":false},
				{"tag_name":"ubuntu-slim/20261012.3","draft":true,"prerelease":false},
				{"tag_name":"ubuntu-slim/20261008.9","draft":false,"prerelease":true},
				{"tag_name":"ubuntu-slim/20261005.17","draft":false,"prerelease":false},
				{"tag_name":"ubuntu-slim/20260925.9","draft":false,"prerelease":false}]`))
		case "/actions/runner-images/ubuntu-slim/20261005.17/images/ubuntu-slim/ubuntu-slim-Report.json":
			_, _ = w.Write([]byte(`{"NodeType":"HeaderNode","Title":"Ubuntu-Slim"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubReleases(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	srv := releasesServer(t, `"notes\n<!-- BEGIN SHA linux-x64 -->`+sha+`<!-- END SHA linux-x64 -->\n<!-- BEGIN SHA linux-arm64 -->ff<!-- END SHA linux-arm64 -->"`)
	r := NewGitHubReleases(srv.URL, srv.URL, srv.Client())
	ctx := context.Background()
	slim, err := r.LatestSlim(ctx)
	if err != nil || slim.Tag != "ubuntu-slim/20261005.17" || slim.Version != "20261005.17" {
		t.Fatalf("slim = %+v, %v", slim, err)
	}
	run, err := r.LatestRunner(ctx)
	if err != nil || run.Version != "2.338.0" || run.SHA256 != sha ||
		run.URL != "https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-linux-x64-2.338.0.tar.gz" {
		t.Fatalf("runner = %+v, %v", run, err)
	}
	rep, err := r.PublishedReport(ctx, slim)
	if err != nil || !strings.Contains(string(rep), "Ubuntu-Slim") {
		t.Fatalf("report = %s, %v", rep, err)
	}
}

func TestRunnerWithoutChecksumIsRefused(t *testing.T) {
	srv := releasesServer(t, `"no checksums here"`)
	if _, err := NewGitHubReleases(srv.URL, srv.URL, srv.Client()).LatestRunner(context.Background()); err == nil {
		t.Fatal("a runner release without a SHA-256 must be refused")
	}
}
