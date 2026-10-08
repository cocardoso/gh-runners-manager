package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/github"
	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/version"
)

// GitHubJobs fetches job details (steps, URL) from GitHub.
type GitHubJobs interface {
	JobDetails(ctx context.Context, scaleSet, repo string, runID int64, runnerName string) (github.JobDetails, error)
}

// Overview is the dashboard summary.
type Overview struct {
	KPIs     KPIs     `json:"kpis"`
	Capacity Capacity `json:"capacity"`
	Alerts   []Alert  `json:"alerts"`
}

// KPIs are the headline numbers.
type KPIs struct {
	RunningJobs           int     `json:"running_jobs"`
	WaitingDemand         int     `json:"waiting_demand"`
	Jobs24h               int     `json:"jobs_24h"`
	SuccessRate24h        float64 `json:"success_rate_24h"`
	MedianQueueSeconds24h float64 `json:"median_queue_seconds_24h"`
}

// Capacity summarises resource usage.
type Capacity struct {
	EnvironmentsLive      int     `json:"environments_live"`
	EnvironmentsMax       int     `json:"environments_max"`
	MemoryCommittedMB     int     `json:"memory_committed_mb"`
	MemoryBudgetMB        int     `json:"memory_budget_mb"`
	HostMemoryAvailableMB int     `json:"host_memory_available_mb"`
	HostMemoryTotalMB     int     `json:"host_memory_total_mb"`
	DiskPercent           float64 `json:"disk_percent"`
	DiskMaxPercent        float64 `json:"disk_max_percent"`
}

// Alert is something the operator should look at.
type Alert struct {
	Level         string    `json:"level"`
	Kind          string    `json:"kind"`
	Message       string    `json:"message"`
	ScaleSet      string    `json:"scale_set,omitempty"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	Time          time.Time `json:"time"`
}

// JobBucket counts jobs that finished in one hour.
type JobBucket struct {
	Start     time.Time `json:"start"`
	Succeeded int       `json:"succeeded"`
	Failed    int       `json:"failed"`
	Canceled  int       `json:"canceled"`
	Other     int       `json:"other"`
}

var liveStates = []string{"pending", "provisioning", "booting", "connected", "idle", "running", "completing", "failed", "destroying"}

func registerOverview(a huma.API, d Deps) {
	huma.Register(a, huma.Operation{OperationID: "get-overview", Method: http.MethodGet, Path: "/api/v1/overview", Summary: "Dashboard summary", Tags: []string{"overview"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body Overview }, error) {
			ov, err := overview(ctx, d, time.Now())
			if err != nil {
				return nil, err
			}
			return &struct{ Body Overview }{Body: ov}, nil
		})

	huma.Register(a, huma.Operation{OperationID: "job-stats", Method: http.MethodGet, Path: "/api/v1/stats/jobs", Summary: "Finished jobs per hour", Tags: []string{"overview"}},
		func(ctx context.Context, in *struct {
			Hours int `query:"hours" minimum:"1" maximum:"168" default:"24"`
		}) (*struct {
			Body struct {
				Buckets []JobBucket `json:"buckets"`
			}
		}, error) {
			hours := in.Hours
			if hours <= 0 {
				hours = 24
			}
			jobs, err := d.Store.ListJobs(ctx, store.JobFilter{Status: "completed", Limit: 10000})
			if err != nil {
				return nil, err
			}
			end := time.Now().Truncate(time.Hour).Add(time.Hour)
			start := end.Add(-time.Duration(hours) * time.Hour)
			buckets := make([]JobBucket, hours)
			for i := range buckets {
				buckets[i].Start = start.Add(time.Duration(i) * time.Hour)
			}
			for _, j := range jobs {
				if j.FinishedAt.Before(start) || !j.FinishedAt.Before(end) {
					continue
				}
				b := &buckets[int(j.FinishedAt.Sub(start)/time.Hour)]
				switch j.Result {
				case "succeeded":
					b.Succeeded++
				case "failed":
					b.Failed++
				case "canceled":
					b.Canceled++
				default:
					b.Other++
				}
			}
			out := &struct {
				Body struct {
					Buckets []JobBucket `json:"buckets"`
				}
			}{}
			out.Body.Buckets = buckets
			return out, nil
		})

	huma.Register(a, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/api/v1/settings", Summary: "Configuration without secrets", Tags: []string{"settings"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body Settings }, error) {
			return &struct{ Body Settings }{Body: settingsView(d)}, nil
		})

	huma.Register(a, huma.Operation{OperationID: "get-job-github", Method: http.MethodGet, Path: "/api/v1/jobs/{id}/github", Summary: "Job steps and URL from GitHub", Tags: []string{"jobs"}},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body github.JobDetails }, error) {
			j, err := d.Store.GetJob(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, huma.Error404NotFound("job not found")
			}
			if err != nil {
				return nil, err
			}
			if d.GitHubJobs == nil || j.RunID == 0 || j.Repository == "" {
				return &struct{ Body github.JobDetails }{Body: github.JobDetails{Reason: "no workflow run is known for this job", Steps: []github.Step{}}}, nil
			}
			det, err := d.GitHubJobs.JobDetails(ctx, j.ScaleSet, j.Repository, j.RunID, j.RunnerName)
			if err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			return &struct{ Body github.JobDetails }{Body: det}, nil
		})
}

func overview(ctx context.Context, d Deps, now time.Time) (Overview, error) {
	var ov Overview
	ov.Alerts = []Alert{}
	if d.Cache != nil {
		if c := d.Cache.Status(); c.Enabled && !c.CheckedAt.IsZero() && !c.Up {
			ov.Alerts = append(ov.Alerts, Alert{Level: "warn", Kind: "cache_down",
				Message: "the registry cache at " + c.Address + " does not answer; jobs pull from the registries directly", Time: c.DownSince})
		}
	}
	running, err := d.Store.ListJobs(ctx, store.JobFilter{Status: "running", Limit: 1000})
	if err != nil {
		return ov, err
	}
	ov.KPIs.RunningJobs = len(running)
	completed, err := d.Store.ListJobs(ctx, store.JobFilter{Status: "completed", Limit: 10000})
	if err != nil {
		return ov, err
	}
	since := now.Add(-24 * time.Hour)
	var waits []float64
	succeeded := 0
	for _, j := range completed {
		if j.FinishedAt.Before(since) {
			continue
		}
		ov.KPIs.Jobs24h++
		if j.Result == "succeeded" {
			succeeded++
		}
		if !j.QueuedAt.IsZero() && j.StartedAt.After(j.QueuedAt) {
			waits = append(waits, j.StartedAt.Sub(j.QueuedAt).Seconds())
		}
	}
	if ov.KPIs.Jobs24h > 0 {
		ov.KPIs.SuccessRate24h = float64(succeeded) / float64(ov.KPIs.Jobs24h)
	}
	if len(waits) > 0 {
		sort.Float64s(waits)
		m := len(waits) / 2
		ov.KPIs.MedianQueueSeconds24h = waits[m]
		if len(waits)%2 == 0 {
			ov.KPIs.MedianQueueSeconds24h = (waits[m-1] + waits[m]) / 2
		}
	}
	if d.Controller != nil {
		for _, s := range d.Controller.ScaleSets(ctx) {
			if s.Desired > s.Live {
				ov.KPIs.WaitingDemand += s.Desired - s.Live
			}
			if s.ListenError != "" {
				ov.Alerts = append(ov.Alerts, Alert{Level: "error", Kind: "listener", ScaleSet: s.Name, Message: "listener stopped: " + s.ListenError, Time: now})
			}
			if s.Waiting != "" {
				ov.Alerts = append(ov.Alerts, Alert{Level: "warn", Kind: "waiting", ScaleSet: s.Name, Message: "jobs are waiting: " + s.Waiting, Time: s.WaitingSince})
			}
		}
	}
	live, err := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: liveStates})
	if err != nil {
		return ov, err
	}
	ov.Capacity.EnvironmentsLive = len(live)
	for _, e := range live {
		ov.Capacity.MemoryCommittedMB += e.MemoryMB
	}
	if d.Config != nil {
		ov.Capacity.EnvironmentsMax = d.Config.Capacity.MaxEnvironments
		ov.Capacity.MemoryBudgetMB = d.Config.Capacity.MemoryBudgetMB
		ov.Capacity.DiskMaxPercent = d.Config.Capacity.MaxDiskPercent
	}
	if d.Capacity != nil {
		if rc, err := d.Capacity(ctx); err == nil {
			ov.Capacity.HostMemoryAvailableMB, ov.Capacity.HostMemoryTotalMB, ov.Capacity.DiskPercent = rc.HostMemoryAvailableMB, rc.HostMemoryTotalMB, rc.ThinPoolPercent
		} else {
			ov.Alerts = append(ov.Alerts, Alert{Level: "error", Kind: "runtime", Message: "the runtime is unreachable: " + err.Error(), Time: now})
		}
	}
	recent, err := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{Limit: 200})
	if err != nil {
		return ov, err
	}
	for _, e := range recent {
		if e.FailureStage != "" && e.UpdatedAt.After(since) {
			ov.Alerts = append(ov.Alerts, Alert{Level: "error", Kind: "environment_failed", ScaleSet: e.ScaleSet, EnvironmentID: e.ID,
				Message: "environment failed at " + e.FailureStage + ": " + e.FailureReason, Time: e.UpdatedAt})
		}
	}
	return ov, nil
}

// Settings is the configuration without secrets.
type Settings struct {
	Version      string            `json:"version"`
	AdminActions bool              `json:"admin_actions"`
	Proxmox      map[string]any    `json:"proxmox"`
	Ingest       map[string]string `json:"ingest"`
	Capacity     map[string]any    `json:"capacity"`
	ScaleSets    []map[string]any  `json:"scale_sets"`
}

func settingsView(d Deps) Settings {
	s := Settings{Version: version.Version, AdminActions: d.AdminToken != "" || d.Auth != nil, ScaleSets: []map[string]any{}}
	c := d.Config
	if c == nil {
		return s
	}
	p := c.Proxmox
	s.Proxmox = map[string]any{"url": p.URL, "node": p.Node, "token_id": p.TokenID, "template_vmid": p.TemplateVMID, "pool": p.Pool,
		"vmid_range": []int{p.VMIDRange.Start, p.VMIDRange.End}, "storage": p.Storage, "thin_pool": p.ThinPool,
		"firewall_settle": p.FirewallSettle.Std().String(), "tls_pinned": p.TLSFingerprint != ""}
	s.Ingest = map[string]string{"listen": c.Ingest.Listen, "advertise_url": c.Ingest.AdvertiseURL}
	s.Capacity = map[string]any{"max_environments": c.Capacity.MaxEnvironments, "memory_budget_mb": c.Capacity.MemoryBudgetMB,
		"memory_margin_mb": c.Capacity.MemoryMarginMB, "max_disk_percent": c.Capacity.MaxDiskPercent}
	for _, ss := range c.ScaleSets {
		s.ScaleSets = append(s.ScaleSets, map[string]any{"name": ss.Name, "url": ss.URL, "credential": ss.Credential, "runner_group": ss.RunnerGroup,
			"labels": ss.Labels, "max_concurrent": ss.MaxConcurrent, "cores": ss.Cores, "memory_mb": ss.MemoryMB, "keep_on_failure_minutes": ss.KeepOnFailureMinutes,
			"warm_runners": ss.WarmRunners})
	}
	return s
}
