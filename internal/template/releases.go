// Package template builds, verifies, activates and retires job templates (spec §8).
package template

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Release is an ubuntu-slim release of actions/runner-images.
type Release struct {
	Tag     string // ubuntu-slim/20261005.17
	Version string // 20261005.17
}

// RunnerRelease is an actions/runner release for linux-x64.
type RunnerRelease struct {
	Version string
	URL     string
	SHA256  string
}

// Releases looks up the inputs of a template build.
type Releases interface {
	LatestSlim(ctx context.Context) (Release, error)
	LatestRunner(ctx context.Context) (RunnerRelease, error)
	// PublishedReport returns the software report GitHub publishes with the slim release.
	PublishedReport(ctx context.Context, slim Release) ([]byte, error)
}

const slimPrefix = "ubuntu-slim/"

var runnerSHA = regexp.MustCompile(`<!-- BEGIN SHA linux-x64 -->([0-9a-f]{64})<!-- END SHA linux-x64 -->`)

type githubReleases struct {
	api, raw, web string
	hc            *http.Client
}

// NewGitHubReleases reads releases from the GitHub REST API (apiBase, default https://api.github.com)
// and files from raw.githubusercontent.com (rawBase). Release assets come from https://github.com,
// or from rawBase when one is given (tests).
func NewGitHubReleases(apiBase, rawBase string, hc *http.Client) Releases {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	web := rawBase
	if rawBase == "" {
		rawBase, web = "https://raw.githubusercontent.com", "https://github.com"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &githubReleases{api: strings.TrimRight(apiBase, "/"), raw: strings.TrimRight(rawBase, "/"), web: strings.TrimRight(web, "/"), hc: hc}
}

func (g *githubReleases) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("template: GET %s: %s", url, resp.Status)
	}
	if b, ok := out.(*[]byte); ok {
		*b = body
		return nil
	}
	return json.Unmarshal(body, out)
}

func (g *githubReleases) LatestSlim(ctx context.Context) (Release, error) {
	var rels []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := g.get(ctx, g.api+"/repos/actions/runner-images/releases?per_page=100", &rels); err != nil {
		return Release{}, err
	}
	var best Release
	for _, r := range rels {
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, slimPrefix) {
			continue
		}
		v := strings.TrimPrefix(r.Tag, slimPrefix)
		if best.Tag == "" || newerSlim(v, best.Version) {
			best = Release{Tag: r.Tag, Version: v}
		}
	}
	if best.Tag == "" {
		return Release{}, fmt.Errorf("template: no ubuntu-slim release found")
	}
	return best, nil
}

// newerSlim compares versions like 20261005.17 (date, then build number).
func newerSlim(a, b string) bool {
	ad, an, _ := strings.Cut(a, ".")
	bd, bn, _ := strings.Cut(b, ".")
	if ad != bd {
		return ad > bd
	}
	if len(an) != len(bn) {
		return len(an) > len(bn)
	}
	return an > bn
}

func (g *githubReleases) LatestRunner(ctx context.Context) (RunnerRelease, error) {
	var rel struct {
		Tag  string `json:"tag_name"`
		Body string `json:"body"`
	}
	if err := g.get(ctx, g.api+"/repos/actions/runner/releases/latest", &rel); err != nil {
		return RunnerRelease{}, err
	}
	v := strings.TrimPrefix(rel.Tag, "v")
	m := runnerSHA.FindStringSubmatch(rel.Body)
	if v == "" || m == nil {
		return RunnerRelease{}, fmt.Errorf("template: runner release %q has no linux-x64 SHA-256; refusing an unverified runner", rel.Tag)
	}
	return RunnerRelease{
		Version: v,
		SHA256:  m[1],
		URL:     fmt.Sprintf("https://github.com/actions/runner/releases/download/v%s/actions-runner-linux-x64-%s.tar.gz", v, v),
	}, nil
}

// The release asset is the report of that very image; the recipe's report file is not
// refreshed for every release, so it is only a fallback.
func (g *githubReleases) PublishedReport(ctx context.Context, slim Release) ([]byte, error) {
	var b []byte
	if err := g.get(ctx, g.web+"/actions/runner-images/releases/download/"+slim.Tag+"/internal.ubuntu-slim.json", &b); err == nil {
		return b, nil
	}
	err := g.get(ctx, g.raw+"/actions/runner-images/"+slim.Tag+"/images/ubuntu-slim/ubuntu-slim-Report.json", &b)
	return b, err
}
