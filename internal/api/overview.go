package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
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
	// MedianDurationSeconds24h is the median run time (start to finish) of the jobs finished in 24 hours.
	MedianDurationSeconds24h float64 `json:"median_duration_seconds_24h"`
	// QueuedJobs are jobs assigned to a scale set that no runner took yet.
	QueuedJobs     int       `json:"queued_jobs"`
	OldestQueuedAt time.Time `json:"oldest_queued_at,omitzero"`
	// PreparingRunners are job environments on their way to a runner; ReadyRunners wait idle for a job.
	PreparingRunners int `json:"preparing_runners"`
	ReadyRunners     int `json:"ready_runners"`
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

// Alert is something the operator should look at. Alerts that repeat (environment
// failures with the same cause) are one alert with a count.
type Alert struct {
	// ID identifies the alert across occurrences, for dismissing it.
	ID      string `json:"id"`
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// ScaleSet and EnvironmentID name where it happened; for a repeated alert, the latest occurrence.
	ScaleSet      string `json:"scale_set,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	// Count is how many times it happened in the last 24 hours (since it was dismissed).
	Count int `json:"count"`
	// FirstTime is the first occurrence counted, Time the latest (or since when a condition holds).
	FirstTime time.Time `json:"first_time"`
	Time      time.Time `json:"time"`
	// Dismissible alerts can be dismissed until they happen again; the others are live
	// conditions that last until they are fixed.
	Dismissible bool `json:"dismissible"`
}

// DismissedAlert names an alert to dismiss and its time as the operator saw it.
type DismissedAlert struct {
	ID   string    `json:"id" minLength:"1"`
	Time time.Time `json:"time"`
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

	huma.Register(a, huma.Operation{OperationID: "dismiss-alerts", Method: http.MethodPost, Path: "/api/v1/alerts/dismiss",
		Summary: "Dismiss overview alerts until they happen again", Tags: []string{"overview"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				Alerts []DismissedAlert `json:"alerts" minItems:"1" maxItems:"200"`
			}
		}) (*struct{}, error) {
			// An alert is dismissed as the operator saw it: what happened after stays visible.
			now := time.Now()
			seen := make(map[string]time.Time, len(in.Body.Alerts))
			for _, a := range in.Body.Alerts {
				at := a.Time
				if at.IsZero() || at.After(now) {
					at = now
				}
				seen[a.ID] = at
			}
			if err := d.Store.DismissAlerts(ctx, seen, now); err != nil {
				return nil, err
			}
			return &struct{}{}, nil
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
			ov.Alerts = append(ov.Alerts, Alert{ID: "cache_down", Level: "warn", Kind: "cache_down", Dismissible: true,
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
	var waits, durations []float64
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
		if !j.StartedAt.IsZero() && j.FinishedAt.After(j.StartedAt) {
			durations = append(durations, j.FinishedAt.Sub(j.StartedAt).Seconds())
		}
	}
	ov.KPIs.MedianDurationSeconds24h = median(durations)
	queued, err := d.Store.ListJobs(ctx, store.JobFilter{Status: "assigned", Limit: 1000})
	if err != nil {
		return ov, err
	}
	ov.KPIs.QueuedJobs = len(queued)
	for _, j := range queued {
		if !j.QueuedAt.IsZero() && (ov.KPIs.OldestQueuedAt.IsZero() || j.QueuedAt.Before(ov.KPIs.OldestQueuedAt)) {
			ov.KPIs.OldestQueuedAt = j.QueuedAt
		}
	}
	if ov.KPIs.Jobs24h > 0 {
		ov.KPIs.SuccessRate24h = float64(succeeded) / float64(ov.KPIs.Jobs24h)
	}
	ov.KPIs.MedianQueueSeconds24h = median(waits)
	if d.Controller != nil {
		for _, s := range d.Controller.ScaleSets(ctx) {
			if s.Desired > s.Live {
				ov.KPIs.WaitingDemand += s.Desired - s.Live
			}
			if s.ListenError != "" {
				ov.Alerts = append(ov.Alerts, Alert{ID: "listener:" + s.Name, Level: "error", Kind: "listener", ScaleSet: s.Name, Message: "listener stopped: " + s.ListenError, Time: now})
			}
			if s.Waiting != "" {
				ov.Alerts = append(ov.Alerts, Alert{ID: "waiting:" + s.Name + ":" + s.Waiting, Level: "warn", Kind: "waiting", ScaleSet: s.Name,
					Message: "jobs are waiting: " + s.Waiting, Time: s.WaitingSince, Dismissible: true})
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
		if e.Kind != "" && e.Kind != store.KindJob {
			continue // a template build or self-test is no runner
		}
		switch e.State {
		case "pending", "provisioning", "booting", "connected":
			ov.KPIs.PreparingRunners++
		case "idle":
			ov.KPIs.ReadyRunners++
		}
	}
	if d.Config != nil || d.Settings != nil {
		limits, _ := d.capacityLimits()
		ov.Capacity.EnvironmentsMax = limits.MaxEnvironments
		ov.Capacity.MemoryBudgetMB = limits.MemoryBudgetMB
		ov.Capacity.DiskMaxPercent = limits.MaxDiskPercent
	}
	if d.Capacity != nil {
		if rc, err := d.Capacity(ctx); err == nil {
			ov.Capacity.HostMemoryAvailableMB, ov.Capacity.HostMemoryTotalMB, ov.Capacity.DiskPercent = rc.HostMemoryAvailableMB, rc.HostMemoryTotalMB, rc.ThinPoolPercent
		} else {
			ov.Alerts = append(ov.Alerts, Alert{ID: "runtime", Level: "error", Kind: "runtime", Message: "the runtime is unreachable: " + err.Error(), Time: now})
		}
	}
	// Failures are rare once provisioning backs off; the limit only bounds a pathological day.
	failures, err := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{FailedSince: since, Limit: 20000})
	if err != nil {
		return ov, err
	}
	dismissed, err := d.Store.AlertDismissals(ctx)
	if err != nil {
		return ov, err
	}
	ov.Alerts = withoutDismissed(ov.Alerts, keyed(failures), dismissed)
	return ov, nil
}

// Parts of failure reasons that name one guest, runner or task: the same failure
// on another guest has the same cause. Codes and statuses are kept.
var volatileParts = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`UPID:\S+`), "UPID"},
	{regexp.MustCompile(`\b(CT|VM|vm|ct)[ -](\d+)\b`), "$1 #"},
	{regexp.MustCompile(`\b(vm|base|subvol)-\d+-disk`), "$1-#-disk"},
	{regexp.MustCompile(`/(lxc|qemu)/\d+`), "/$1/#"},
	{regexp.MustCompile(`\bghrm-[0-9a-z]{12}\b`), "ghrm-RUNNER"},
	{regexp.MustCompile(`\b[0-9a-z]{26}\b`), "ID"},
	{regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`), "IP"},
}

// failureKey identifies environment failures with the same cause: the same scale set,
// stage and reason, ignoring the guest, runner and task it happened to.
func failureKey(e store.Environment) string {
	reason := e.FailureReason
	for _, p := range volatileParts {
		reason = p.re.ReplaceAllString(reason, p.with)
	}
	sum := sha256.Sum256([]byte(e.ScaleSet + "\x00" + e.FailureStage + "\x00" + reason))
	return "environment_failed:" + hex.EncodeToString(sum[:8])
}

// failure is an environment failure with its cause.
type failure struct {
	key string
	env store.Environment
}

func keyed(envs []store.Environment) []failure {
	out := make([]failure, len(envs))
	for i, e := range envs {
		out[i] = failure{failureKey(e), e}
	}
	return out
}

// failureAlerts groups environment failures (any order) into one alert per cause, the
// most recent first.
func failureAlerts(failures []failure) []Alert {
	byKey := map[string]*Alert{}
	var order []string
	for _, f := range failures {
		e := f.env
		a, ok := byKey[f.key]
		if !ok {
			a = &Alert{ID: f.key, Level: "error", Kind: "environment_failed", Dismissible: true}
			byKey[f.key] = a
			order = append(order, f.key)
		}
		a.Count++
		if a.FirstTime.IsZero() || e.FailedAt.Before(a.FirstTime) {
			a.FirstTime = e.FailedAt
		}
		if !e.FailedAt.Before(a.Time) {
			a.Time, a.ScaleSet, a.EnvironmentID = e.FailedAt, e.ScaleSet, e.ID
			a.Message = "environment failed at " + e.FailureStage + ": " + e.FailureReason
		}
	}
	out := make([]Alert, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// withoutDismissed drops dismissed alerts that did not happen again since; failures
// are grouped from those after their group's dismissal.
func withoutDismissed(alerts []Alert, failures []failure, dismissed map[string]time.Time) []Alert {
	var recent []failure
	for _, f := range failures {
		if at, ok := dismissed[f.key]; !ok || f.env.FailedAt.After(at) {
			recent = append(recent, f)
		}
	}
	out := make([]Alert, 0, len(alerts))
	for _, a := range alerts {
		// Dismissals are stored in milliseconds; alert times can be finer.
		if at, ok := dismissed[a.ID]; ok && a.Dismissible && !a.Time.Truncate(time.Millisecond).After(at) {
			continue
		}
		out = append(out, a)
	}
	return append(out, failureAlerts(recent)...)
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
	limits, src := d.capacityLimits()
	s.Capacity = map[string]any{"max_environments": limits.MaxEnvironments, "memory_budget_mb": limits.MemoryBudgetMB,
		"memory_margin_mb": limits.MemoryMarginMB, "max_disk_percent": limits.MaxDiskPercent, "source": src}
	for _, ss := range c.ScaleSets {
		s.ScaleSets = append(s.ScaleSets, map[string]any{"name": ss.Name, "url": ss.URL, "credential": ss.Credential, "runner_group": ss.RunnerGroup,
			"labels": ss.Labels, "max_concurrent": ss.MaxConcurrent, "cores": ss.Cores, "memory_mb": ss.MemoryMB, "keep_on_failure_minutes": ss.KeepOnFailureMinutes,
			"warm_runners": ss.WarmRunners, "template_profile": ss.TemplateProfile})
	}
	return s
}

// median returns the median of xs (0 for none); it sorts xs.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	m := len(xs) / 2
	if len(xs)%2 == 0 {
		return (xs[m-1] + xs[m]) / 2
	}
	return xs[m]
}
