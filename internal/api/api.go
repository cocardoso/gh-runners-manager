// Package api serves the read-mostly REST API and the SSE streams (spec §7, §11).
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/version"
)

// Controller is what the API needs from the controller.
type Controller interface {
	ScaleSets(ctx context.Context) []controller.ScaleSetStatus
	RequestDestroy(ctx context.Context, id string) error
}

// Deps are the API's collaborators.
type Deps struct {
	Store      *store.Store
	Recorder   *events.Recorder
	Logs       *logs.Store
	Controller Controller
	AdminToken string
	// Ready, when set, is called by /readyz in addition to the store ping.
	Ready func(ctx context.Context) error
	// UI, when set, serves every path that is not an API path.
	UI http.Handler
	// Config is shown (without secrets) on the settings endpoint and used for limits.
	Config *config.Config
	// Capacity reports runtime capacity for the overview.
	Capacity func(ctx context.Context) (runtime.Capacity, error)
	// GitHubJobs fetches job steps from GitHub.
	GitHubJobs GitHubJobs
}

// Environment is the API view of an environment.
type Environment struct {
	ID             string            `json:"id"`
	ScaleSet       string            `json:"scale_set"`
	State          string            `json:"state"`
	RuntimeRef     string            `json:"runtime_ref,omitempty"`
	RunnerName     string            `json:"runner_name,omitempty"`
	RunnerID       int64             `json:"runner_id,omitempty"`
	IP             string            `json:"ip,omitempty"`
	FailureStage   string            `json:"failure_stage,omitempty"`
	FailureReason  string            `json:"failure_reason,omitempty"`
	JobID          string            `json:"job_id,omitempty"`
	ExitCode       *int              `json:"exit_code,omitempty"`
	MemoryMB       int               `json:"memory_mb"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	StateChangedAt time.Time         `json:"state_changed_at"`
	LogStreams     []store.LogStream `json:"log_streams,omitempty"`
}

// Job is the API view of a job.
type Job struct {
	ID            string    `json:"id"`
	ScaleSet      string    `json:"scale_set"`
	Repository    string    `json:"repository"`
	WorkflowRef   string    `json:"workflow_ref,omitempty"`
	DisplayName   string    `json:"display_name"`
	EventName     string    `json:"event_name,omitempty"`
	RunID         int64     `json:"run_id,omitempty"`
	RunnerName    string    `json:"runner_name,omitempty"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	Status        string    `json:"status"`
	Result        string    `json:"result,omitempty"`
	QueuedAt      time.Time `json:"queued_at"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ScaleSet is the API view of a scale set.
type ScaleSet struct {
	Name         string    `json:"name"`
	GitHubID     int       `json:"github_id"`
	Desired      int       `json:"desired"`
	Live         int       `json:"live"`
	Waiting      string    `json:"waiting,omitempty"`
	WaitingSince time.Time `json:"waiting_since"`
	Listening    bool      `json:"listening"`
	ListenError  string    `json:"listen_error,omitempty"`
}

func toEnvironment(e store.Environment) Environment {
	return Environment{ID: e.ID, ScaleSet: e.ScaleSet, State: e.State, RuntimeRef: e.RuntimeRef, RunnerName: e.RunnerName,
		RunnerID: e.RunnerID, IP: e.IP, FailureStage: e.FailureStage, FailureReason: e.FailureReason, JobID: e.JobID,
		ExitCode: e.ExitCode, MemoryMB: e.MemoryMB, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, StateChangedAt: e.StateChangedAt}
}

func toJob(j store.Job) Job {
	return Job{ID: j.ID, ScaleSet: j.ScaleSet, Repository: j.Repository, WorkflowRef: j.WorkflowRef, DisplayName: j.DisplayName,
		EventName: j.EventName, RunID: j.RunID, RunnerName: j.RunnerName, EnvironmentID: j.EnvironmentID, Status: j.Status,
		Result: j.Result, QueuedAt: j.QueuedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, UpdatedAt: j.UpdatedAt}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func limit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// New returns the HTTP handler for the API (and the UI when configured).
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("gh-runners-manager API", version.Version)
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = "/api/docs"
	cfg.SchemasPath = "/api/schemas"
	a := humago.New(mux, cfg)

	huma.Register(a, huma.Operation{OperationID: "list-scale-sets", Method: http.MethodGet, Path: "/api/v1/scale-sets", Summary: "List scale sets", Tags: []string{"scale sets"}},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				ScaleSets []ScaleSet `json:"scale_sets"`
			}
		}, error) {
			out := &struct {
				Body struct {
					ScaleSets []ScaleSet `json:"scale_sets"`
				}
			}{}
			out.Body.ScaleSets = []ScaleSet{}
			for _, s := range d.Controller.ScaleSets(ctx) {
				out.Body.ScaleSets = append(out.Body.ScaleSets, ScaleSet{Name: s.Name, GitHubID: s.GitHubID, Desired: s.Desired, Live: s.Live,
					Waiting: s.Waiting, WaitingSince: s.WaitingSince, Listening: s.Listening, ListenError: s.ListenError})
			}
			return out, nil
		})

	type listEnvironmentsInput struct {
		State    string `query:"state" doc:"Comma-separated states"`
		ScaleSet string `query:"scale_set"`
		Limit    int    `query:"limit" minimum:"0" maximum:"1000"`
	}
	huma.Register(a, huma.Operation{OperationID: "list-environments", Method: http.MethodGet, Path: "/api/v1/environments", Summary: "List environments, newest first", Tags: []string{"environments"}},
		func(ctx context.Context, in *listEnvironmentsInput) (*struct {
			Body struct {
				Environments []Environment `json:"environments"`
			}
		}, error) {
			es, err := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: splitList(in.State), ScaleSet: in.ScaleSet, Limit: limit(in.Limit, 200, 1000)})
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					Environments []Environment `json:"environments"`
				}
			}{}
			out.Body.Environments = make([]Environment, 0, len(es))
			for _, e := range es {
				out.Body.Environments = append(out.Body.Environments, toEnvironment(e))
			}
			return out, nil
		})

	type idInput struct {
		ID string `path:"id"`
	}
	huma.Register(a, huma.Operation{OperationID: "get-environment", Method: http.MethodGet, Path: "/api/v1/environments/{id}", Summary: "Get an environment with its log streams", Tags: []string{"environments"}},
		func(ctx context.Context, in *idInput) (*struct{ Body Environment }, error) {
			e, err := d.Store.GetEnvironment(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, huma.Error404NotFound("environment not found")
			}
			if err != nil {
				return nil, err
			}
			out := &struct{ Body Environment }{Body: toEnvironment(e)}
			out.Body.LogStreams, _ = d.Store.ListLogStreams(ctx, e.ID)
			return out, nil
		})

	huma.Register(a, huma.Operation{OperationID: "destroy-environment", Method: http.MethodPost, Path: "/api/v1/environments/{id}/destroy",
		Summary: "Destroy an environment (admin)", Tags: []string{"environments"}, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *struct {
			ID            string `path:"id"`
			Authorization string `header:"Authorization"`
		}) (*struct{}, error) {
			if d.AdminToken == "" {
				return nil, huma.Error403Forbidden("mutating calls are disabled: no admin token is configured")
			}
			token, _ := strings.CutPrefix(in.Authorization, "Bearer ")
			if subtle.ConstantTimeCompare([]byte(token), []byte(d.AdminToken)) != 1 {
				return nil, huma.Error401Unauthorized("invalid admin token")
			}
			e, err := d.Store.GetEnvironment(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, huma.Error404NotFound("environment not found")
			}
			if err != nil {
				return nil, err
			}
			_, _ = d.Recorder.Warn(ctx, "audit.destroy", "destroy requested through the API",
				events.Refs{ScaleSet: e.ScaleSet, EnvironmentID: e.ID, JobID: e.JobID}, nil)
			if err := d.Controller.RequestDestroy(ctx, in.ID); err != nil {
				return nil, huma.Error409Conflict(err.Error())
			}
			return &struct{}{}, nil
		})

	type listJobsInput struct {
		Status   string `query:"status"`
		ScaleSet string `query:"scale_set"`
		Limit    int    `query:"limit" minimum:"0" maximum:"1000"`
	}
	huma.Register(a, huma.Operation{OperationID: "list-jobs", Method: http.MethodGet, Path: "/api/v1/jobs", Summary: "List jobs, most recent first", Tags: []string{"jobs"}},
		func(ctx context.Context, in *listJobsInput) (*struct {
			Body struct {
				Jobs []Job `json:"jobs"`
			}
		}, error) {
			js, err := d.Store.ListJobs(ctx, store.JobFilter{Status: in.Status, ScaleSet: in.ScaleSet, Limit: limit(in.Limit, 200, 1000)})
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					Jobs []Job `json:"jobs"`
				}
			}{}
			out.Body.Jobs = make([]Job, 0, len(js))
			for _, j := range js {
				out.Body.Jobs = append(out.Body.Jobs, toJob(j))
			}
			return out, nil
		})

	huma.Register(a, huma.Operation{OperationID: "get-job", Method: http.MethodGet, Path: "/api/v1/jobs/{id}", Summary: "Get a job", Tags: []string{"jobs"}},
		func(ctx context.Context, in *idInput) (*struct{ Body Job }, error) {
			j, err := d.Store.GetJob(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, huma.Error404NotFound("job not found")
			}
			if err != nil {
				return nil, err
			}
			return &struct{ Body Job }{Body: toJob(j)}, nil
		})

	type listEventsInput struct {
		After       int64  `query:"after" minimum:"0"`
		Before      int64  `query:"before" minimum:"0" doc:"Only events with a lower sequence number"`
		Newest      bool   `query:"newest" doc:"Return the newest matching events (still in ascending order)"`
		Environment string `query:"environment"`
		Job         string `query:"job"`
		Limit       int    `query:"limit" minimum:"0" maximum:"5000"`
	}
	huma.Register(a, huma.Operation{OperationID: "list-events", Method: http.MethodGet, Path: "/api/v1/events", Summary: "List events in sequence order", Tags: []string{"events"}},
		func(ctx context.Context, in *listEventsInput) (*struct {
			Body struct {
				Events []store.Event `json:"events"`
			}
		}, error) {
			evs, err := d.Store.ListEvents(ctx, store.EventFilter{AfterSeq: in.After, BeforeSeq: in.Before, Newest: in.Newest, EnvironmentID: in.Environment, JobID: in.Job, Limit: limit(in.Limit, 1000, 5000)})
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					Events []store.Event `json:"events"`
				}
			}{}
			out.Body.Events = evs
			if out.Body.Events == nil {
				out.Body.Events = []store.Event{}
			}
			return out, nil
		})

	type logsInput struct {
		ID     string `path:"id"`
		Stream string `path:"stream" enum:"control-plane,runtime,agent,runner,job,metrics,build,selftest"`
		Offset int64  `query:"offset" minimum:"0"`
		Tail   bool   `query:"tail" doc:"Return the last entries ending at before (default: the end of the stream)"`
		Before int64  `query:"before" minimum:"-1" default:"-1"`
		Limit  int    `query:"limit" minimum:"0" maximum:"10000"`
	}
	huma.Register(a, huma.Operation{OperationID: "read-logs", Method: http.MethodGet, Path: "/api/v1/environments/{id}/logs/{stream}",
		Summary: "Read a log stream page (add follow=true for a live SSE stream)", Tags: []string{"logs"}},
		func(ctx context.Context, in *logsInput) (*struct {
			Body struct {
				Entries []logs.Entry `json:"entries"`
				Next    int64        `json:"next"`
				// FirstLine is the line number of the first entry of a tail page read from the end (0 otherwise).
				FirstLine int64 `json:"first_line,omitempty"`
			}
		}, error) {
			var entries []logs.Entry
			var next, firstLine int64
			var err error
			if in.Tail && in.Before < 0 {
				var t logs.Tail
				t, err = d.Logs.ReadTail(ctx, in.ID, in.Stream, limit(in.Limit, 2000, 10000))
				entries, next, firstLine = t.Entries, t.Next, t.FirstLine
			} else if in.Tail {
				entries, next, err = d.Logs.ReadBefore(ctx, in.ID, in.Stream, in.Before, limit(in.Limit, 2000, 10000))
			} else {
				entries, next, err = d.Logs.Read(ctx, in.ID, in.Stream, in.Offset, limit(in.Limit, 2000, 10000))
			}
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			out := &struct {
				Body struct {
					Entries   []logs.Entry `json:"entries"`
					Next      int64        `json:"next"`
					FirstLine int64        `json:"first_line,omitempty"`
				}
			}{}
			out.Body.Entries, out.Body.Next, out.Body.FirstLine = entries, next, firstLine
			if out.Body.Entries == nil {
				out.Body.Entries = []logs.Entry{}
			}
			return out, nil
		})

	registerOverview(a, d)

	s := &sse{d: d}
	mux.HandleFunc("GET /api/v1/events/stream", s.events)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		err := d.Store.Ping(r.Context())
		if err == nil && d.Ready != nil {
			err = d.Ready(r.Context())
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	if d.UI != nil {
		mux.Handle("/", d.UI) // the UI handler answers 404 for unknown /api/ paths
	}

	// follow=true on the logs endpoint switches to SSE before huma sees the request.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("follow") == "true" && strings.HasPrefix(r.URL.Path, "/api/v1/environments/") {
			if parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/environments/"), "/"); len(parts) == 3 && parts[1] == "logs" {
				s.logs(w, r, parts[0], parts[2])
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
