package api

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func TestListRepositories(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	if err := h.reg.PutCredential(ctx, "home", "github_pat_x"); err != nil {
		t.Fatal(err)
	}
	// A second scale set on the same repository (trailing slash), and an organization.
	for _, ss := range []config.ScaleSet{
		{Name: "lab-big", URL: "https://github.com/o/r/", Credential: "home"},
		{Name: "org", URL: "https://github.com/acme", Credential: "home"},
	} {
		if err := h.reg.PutScaleSet(ctx, ss); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for _, j := range []store.Job{
		{ID: "j1", ScaleSet: "lab", Repository: "o/r", Status: "completed", Result: "succeeded", FinishedAt: now.Add(-time.Hour)},
		{ID: "j2", ScaleSet: "lab-big", Repository: "o/r", Status: "completed", Result: "failed", FinishedAt: now.Add(-2 * time.Hour)},
		{ID: "j3", ScaleSet: "lab", Repository: "o/r", Status: "running", DisplayName: "build"},
		{ID: "j4", ScaleSet: "org", Repository: "acme/web", Status: "completed", Result: "succeeded", FinishedAt: now.Add(-time.Hour)},
		{ID: "j5", ScaleSet: "org", Repository: "acme/api", Status: "assigned"},
		{ID: "j6", ScaleSet: "gone", Repository: "x/y", Status: "running"},
	} {
		if err := h.db.UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}

	var out struct {
		Repositories []struct {
			URL              string   `json:"url"`
			Owner            string   `json:"owner"`
			Repo             string   `json:"repo"`
			Kind             string   `json:"kind"`
			ScaleSets        []string `json:"scale_sets"`
			Credentials      []string `json:"credentials"`
			JobsRunning      int      `json:"jobs_running"`
			Jobs24h          int      `json:"jobs_24h"`
			Succeeded24h     int      `json:"succeeded_24h"`
			Failed24h        int      `json:"failed_24h"`
			RepositoriesSeen []string `json:"repositories_seen"`
			LastJob          *struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
				Status      string `json:"status"`
			} `json:"last_job"`
		}
	}
	if code := h.getJSON(t, "/api/v1/repositories", &out); code != 200 || len(out.Repositories) != 2 {
		t.Fatalf("repositories = %d %+v", code, out)
	}
	org, repo := out.Repositories[0], out.Repositories[1]
	if org.URL != "https://github.com/acme" || org.Owner != "acme" || org.Repo != "" || org.Kind != "organization" ||
		org.JobsRunning != 1 || org.Jobs24h != 1 || org.Succeeded24h != 1 || org.LastJob == nil {
		t.Fatalf("organization = %+v", org)
	}
	if want := []string{"acme/api", "acme/web"}; !reflect.DeepEqual(org.RepositoriesSeen, want) {
		t.Fatalf("organization repositories seen = %v, want %v", org.RepositoriesSeen, want)
	}
	if repo.URL != "https://github.com/o/r" || repo.Owner != "o" || repo.Repo != "r" || repo.Kind != "repository" ||
		!reflect.DeepEqual(repo.ScaleSets, []string{"lab", "lab-big"}) || !reflect.DeepEqual(repo.Credentials, []string{"c", "home"}) ||
		repo.JobsRunning != 1 || repo.Jobs24h != 2 || repo.Succeeded24h != 1 || repo.Failed24h != 1 || repo.RepositoriesSeen != nil {
		t.Fatalf("repository = %+v", repo)
	}
	if repo.LastJob == nil || repo.LastJob.ID != "j3" || repo.LastJob.DisplayName != "build" || repo.LastJob.Status != "running" {
		t.Fatalf("repository last job = %+v", repo.LastJob)
	}
}

func TestListRepositoriesWithoutJobs(t *testing.T) {
	h := newHarness(t, "")
	var out struct {
		Repositories []map[string]any
	}
	if code := h.getJSON(t, "/api/v1/repositories", &out); code != 200 || len(out.Repositories) != 1 {
		t.Fatalf("repositories = %d %+v", code, out)
	}
	r := out.Repositories[0]
	if r["kind"] != "repository" || r["jobs_running"] != float64(0) || r["last_job"] != nil {
		t.Fatalf("repository = %+v", r)
	}
}
