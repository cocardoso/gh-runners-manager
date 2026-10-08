package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/config"
)

// maxRepositoriesSeen caps the repositories listed for an organization.
const maxRepositoriesSeen = 50

// Repository is a GitHub repository or organization that scale sets serve, with what its
// jobs did recently.
type Repository struct {
	URL         string   `json:"url"`
	Owner       string   `json:"owner"`
	Repo        string   `json:"repo" doc:"Empty for an organization"`
	Kind        string   `json:"kind" enum:"repository,organization"`
	ScaleSets   []string `json:"scale_sets"`
	Credentials []string `json:"credentials"`
	// JobsRunning counts the jobs assigned or running now.
	JobsRunning int `json:"jobs_running"`
	// Jobs24h, Succeeded24h and Failed24h count the jobs completed in the last 24 hours.
	Jobs24h      int            `json:"jobs_24h"`
	Succeeded24h int            `json:"succeeded_24h"`
	Failed24h    int            `json:"failed_24h"`
	LastJob      *RepositoryJob `json:"last_job,omitempty"`
	// RepositoriesSeen (organizations only) are the repositories of the jobs of the last 7 days.
	RepositoriesSeen []string `json:"repositories_seen,omitempty"`
	// RepositoriesSeenTotal is how many there are (the list above is capped).
	RepositoriesSeenTotal int `json:"repositories_seen_total,omitempty"`
}

// RepositoryJob is the most recent job of a repository or organization.
type RepositoryJob struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	Result      string    `json:"result,omitempty"`
	Repository  string    `json:"repository"`
	UpdatedAt   time.Time `json:"updated_at"`
	FinishedAt  time.Time `json:"finished_at"`
}

func registerRepositories(a huma.API, d Deps) {
	huma.Register(a, huma.Operation{OperationID: "list-repositories", Method: http.MethodGet, Path: "/api/v1/repositories",
		Summary: "Repositories and organizations the scale sets serve", Tags: []string{"repositories"}},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Repositories []Repository `json:"repositories"`
			}
		}, error) {
			repos, err := repositories(ctx, d, time.Now())
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					Repositories []Repository `json:"repositories"`
				}
			}{}
			out.Body.Repositories = repos
			return out, nil
		})
}

func configuredScaleSets(d Deps) []config.ScaleSet {
	if d.Settings != nil {
		return d.Settings.ScaleSetConfigs()
	}
	if d.Config != nil {
		return d.Config.ScaleSets
	}
	return nil
}

func repositories(ctx context.Context, d Deps, now time.Time) ([]Repository, error) {
	activity, err := d.Store.ScaleSetActivity(ctx, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	byKey := map[string]*Repository{}
	var order []string
	seen := map[string]map[string]bool{}
	for _, ss := range configuredScaleSets(d) {
		owner, repo, err := ss.OwnerRepo()
		if err != nil {
			continue
		}
		key := strings.ToLower(owner + "/" + repo)
		r := byKey[key]
		if r == nil {
			r = &Repository{URL: "https://github.com/" + owner, Owner: owner, Repo: repo, Kind: "organization", ScaleSets: []string{}, Credentials: []string{}}
			if repo != "" {
				r.URL += "/" + repo
				r.Kind = "repository"
			}
			byKey[key] = r
			order = append(order, key)
			seen[key] = map[string]bool{}
		}
		r.ScaleSets = append(r.ScaleSets, ss.Name)
		if ss.Credential != "" && !slices.Contains(r.Credentials, ss.Credential) {
			r.Credentials = append(r.Credentials, ss.Credential)
		}
		act, ok := activity[ss.Name]
		if !ok {
			continue
		}
		r.JobsRunning += act.Running
		r.Jobs24h += act.Jobs
		r.Succeeded24h += act.Succeeded
		r.Failed24h += act.Failed
		if j := act.LastJob; j.ID != "" && (r.LastJob == nil || j.UpdatedAt.After(r.LastJob.UpdatedAt)) {
			r.LastJob = &RepositoryJob{ID: j.ID, DisplayName: j.DisplayName, Status: j.Status, Result: j.Result,
				Repository: j.Repository, UpdatedAt: j.UpdatedAt, FinishedAt: j.FinishedAt}
		}
		if r.Kind == "organization" {
			for _, name := range act.Repositories {
				seen[key][name] = true
			}
		}
	}
	out := make([]Repository, 0, len(order))
	for _, key := range order {
		r := byKey[key]
		sort.Strings(r.ScaleSets)
		sort.Strings(r.Credentials)
		for name := range seen[key] {
			r.RepositoriesSeen = append(r.RepositoriesSeen, name)
		}
		sort.Strings(r.RepositoriesSeen)
		r.RepositoriesSeenTotal = len(r.RepositoriesSeen)
		if len(r.RepositoriesSeen) > maxRepositoriesSeen {
			r.RepositoriesSeen = r.RepositoriesSeen[:maxRepositoriesSeen]
		}
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Owner+"/"+out[i].Repo), strings.ToLower(out[j].Owner+"/"+out[j].Repo)
		return a < b
	})
	return out, nil
}
