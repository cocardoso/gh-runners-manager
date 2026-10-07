package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Step is one step of a GitHub Actions job.
type Step struct {
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// JobDetails is what the REST API says about a job. Available is false (with a
// Reason) when it cannot be fetched, for example without the Actions: read permission.
type JobDetails struct {
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	ID         int64  `json:"id,omitempty"`
	URL        string `json:"url,omitempty"`
	Status     string `json:"status,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	Steps      []Step `json:"steps"`
}

// REST is a minimal GitHub REST client.
type REST struct {
	BaseURL string // default https://api.github.com
	HTTP    *http.Client
}

// JobDetails finds the job that ran on runnerName in a workflow run.
func (r *REST) JobDetails(ctx context.Context, token, repo string, runID int64, runnerName string) (JobDetails, error) {
	base := r.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/actions/runs/%d/jobs?per_page=100&filter=latest", base, repo, runID), nil)
	if err != nil {
		return JobDetails{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := hc.Do(req)
	if err != nil {
		return JobDetails{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusUnauthorized:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || bytes.Contains(bytes.ToLower(body), []byte("rate limit")) {
			return JobDetails{Reason: "the GitHub API rate limit is exhausted; try again later", Steps: []Step{}}, nil
		}
		return JobDetails{Reason: "the GitHub credential cannot read workflow runs; grant it the Actions: read permission", Steps: []Step{}}, nil
	case http.StatusNotFound:
		return JobDetails{Reason: "the workflow run was not found (or the credential cannot see it)", Steps: []Step{}}, nil
	default:
		return JobDetails{}, fmt.Errorf("github: list jobs of run %d: %s", runID, resp.Status)
	}
	var out struct {
		Jobs []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			RunnerName string `json:"runner_name"`
			HTMLURL    string `json:"html_url"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Steps      []Step `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return JobDetails{}, fmt.Errorf("github: decode jobs: %w", err)
	}
	for _, j := range out.Jobs {
		if j.RunnerName == runnerName {
			steps := j.Steps
			if steps == nil {
				steps = []Step{}
			}
			return JobDetails{Available: true, ID: j.ID, URL: j.HTMLURL, Status: j.Status, Conclusion: j.Conclusion, Steps: steps}, nil
		}
	}
	return JobDetails{Reason: "the job was not found in the workflow run", Steps: []Step{}}, nil
}

// JobDetails looks a job up with the credential of its scale set.
func (c *Client) JobDetails(ctx context.Context, scaleSet, repo string, runID int64, runnerName string) (JobDetails, error) {
	var token string
	for _, s := range c.cfg.ScaleSets {
		if s.Name == scaleSet {
			if cred, ok := c.cfg.Credential(s.Credential); ok {
				token = cred.Token
			}
		}
	}
	if token == "" {
		return JobDetails{Reason: "no credential is configured for scale set " + scaleSet, Steps: []Step{}}, nil
	}
	return (&REST{}).JobDetails(ctx, token, repo, runID, runnerName)
}
