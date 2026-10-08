package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Target is a repository or an organization a token can register runners for.
type Target struct {
	Kind     string `json:"kind" enum:"repository,organization"`
	Owner    string `json:"owner"`
	Name     string `json:"name,omitempty"` // repository name; empty for an organization
	FullName string `json:"full_name"`      // owner/name, or owner
	URL      string `json:"url"`            // what a scale set's url takes
	Private  bool   `json:"private"`
}

// maxTargetPages caps the listing (100 repositories a page).
const maxTargetPages = 10

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Targets lists the repositories a token can see and the organizations they belong to,
// organizations first, then repositories, each sorted by name.
func (r *REST) Targets(ctx context.Context, token string) ([]Target, error) {
	base, hc := r.base()
	url := base + "/user/repos?per_page=100&sort=full_name"
	repos := map[string]Target{}
	orgs := map[string]Target{}
	for page := 0; url != "" && page < maxTargetPages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(body, &e)
			if e.Message == "" {
				e.Message = resp.Status
			}
			return nil, fmt.Errorf("github: %s", e.Message)
		}
		var list []struct {
			FullName string `json:"full_name"`
			Name     string `json:"name"`
			Private  bool   `json:"private"`
			HTMLURL  string `json:"html_url"`
			Owner    struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"owner"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("github: repositories: %w", err)
		}
		for _, x := range list {
			repos[x.FullName] = Target{Kind: "repository", Owner: x.Owner.Login, Name: x.Name, FullName: x.FullName, URL: x.HTMLURL, Private: x.Private}
			// Runners can be registered for an organization, not for a personal account.
			if x.Owner.Type == "Organization" {
				orgs[x.Owner.Login] = Target{Kind: "organization", Owner: x.Owner.Login, FullName: x.Owner.Login, URL: "https://github.com/" + x.Owner.Login}
			}
		}
		url = ""
		if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
			url = m[1]
		}
	}
	out := make([]Target, 0, len(orgs)+len(repos))
	for _, set := range []map[string]Target{orgs, repos} {
		start := len(out)
		for _, t := range set {
			out = append(out, t)
		}
		sort.Slice(out[start:], func(i, j int) bool {
			return strings.ToLower(out[start+i].FullName) < strings.ToLower(out[start+j].FullName)
		})
	}
	return out, nil
}
