package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
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

// TargetList is what a token can reach. Truncated means GitHub had more repositories
// than the listing reads (maxTargetPages pages).
type TargetList struct {
	Targets   []Target `json:"targets"`
	Truncated bool     `json:"truncated"`
}

// maxTargetPages caps the listing (100 repositories a page).
const maxTargetPages = 10

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Targets lists the repositories a token can register runners for (it administers them)
// and their organizations, organizations first, then repositories, each sorted by name.
// When the token may read its organization memberships, only the organizations it
// administers are kept.
func (r *REST) Targets(ctx context.Context, token string) (TargetList, error) {
	base, _ := r.base()
	next := base + "/user/repos?per_page=100&sort=full_name"
	repos := map[string]Target{}
	orgs := map[string]Target{}
	var out TargetList
	for page := 0; next != ""; page++ {
		if page == maxTargetPages {
			out.Truncated = true
			break
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
			Permissions struct {
				Admin bool `json:"admin"`
			} `json:"permissions"`
		}
		link, err := r.getJSON(ctx, token, next, &list)
		if err != nil {
			return TargetList{}, err
		}
		for _, x := range list {
			// Registering a runner takes a repository's administration.
			if !x.Permissions.Admin {
				continue
			}
			repos[x.FullName] = Target{Kind: "repository", Owner: x.Owner.Login, Name: x.Name, FullName: x.FullName, URL: x.HTMLURL, Private: x.Private}
			// Runners can be registered for an organization, not for a personal account.
			if x.Owner.Type == "Organization" {
				orgs[x.Owner.Login] = Target{Kind: "organization", Owner: x.Owner.Login, FullName: x.Owner.Login, URL: strings.TrimSuffix(x.HTMLURL, "/"+x.Name)}
			}
		}
		next = ""
		if m := nextLink.FindStringSubmatch(link); m != nil {
			if !sameOrigin(base, m[1]) {
				return TargetList{}, fmt.Errorf("github: the next page is on another host: %s", m[1])
			}
			next = m[1]
		}
	}
	if len(orgs) > 0 {
		var memberships []struct {
			Role         string `json:"role"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		}
		// A fine-grained token may not read memberships: then every organization stays.
		if _, err := r.getJSON(ctx, token, base+"/user/memberships/orgs?state=active&per_page=100", &memberships); err == nil {
			admin := map[string]bool{}
			for _, m := range memberships {
				admin[m.Organization.Login] = m.Role == "admin"
			}
			for login := range orgs {
				if !admin[login] {
					delete(orgs, login)
				}
			}
		}
	}
	out.Targets = make([]Target, 0, len(orgs)+len(repos))
	for _, set := range []map[string]Target{orgs, repos} {
		start := len(out.Targets)
		for _, t := range set {
			out.Targets = append(out.Targets, t)
		}
		sort.Slice(out.Targets[start:], func(i, j int) bool {
			return strings.ToLower(out.Targets[start+i].FullName) < strings.ToLower(out.Targets[start+j].FullName)
		})
	}
	return out, nil
}

// getJSON reads one GitHub API page into v and returns its Link header.
func (r *REST) getJSON(ctx context.Context, token, url string, v any) (string, error) {
	_, hc := r.base()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("github: %s", e.Message)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return "", fmt.Errorf("github: %w", err)
	}
	return resp.Header.Get("Link"), nil
}

// sameOrigin reports whether next has base's scheme and host, so the token goes nowhere else.
func sameOrigin(base, next string) bool {
	b, err1 := neturl.Parse(base)
	n, err2 := neturl.Parse(next)
	return err1 == nil && err2 == nil && b.Scheme == n.Scheme && b.Host == n.Host
}
