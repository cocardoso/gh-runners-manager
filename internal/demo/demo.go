// Package demo runs the control plane with a fake runtime, a simulated GitHub and
// simulated agents, so the API and the UI can be developed and tested without
// Proxmox or GitHub.
package demo

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/environment"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/ids"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/runtime/runtimetest"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Options tune the simulation.
type Options struct {
	DataDir    string
	Seed       int64
	Tick       time.Duration // simulation step (default 500ms)
	JobSeconds [2]float64    // min/max simulated job duration (default 8–40s)
	// IdleTimeout releases environments that got no job (default 20s; production uses 10m).
	IdleTimeout time.Duration
}

// Demo is a running simulation.
type Demo struct {
	Store      *store.Store
	Recorder   *events.Recorder
	Logs       *logs.Store
	Runtime    *runtimetest.Fake
	Controller *controller.Controller
	Config     *config.Config

	opts Options
	rng  *rand.Rand

	mu       sync.Mutex
	scalers  map[string]listener.Scaler
	queued   map[string][]simJob
	running  map[string]*simJob // by environment ID
	booted   map[string]time.Time
	stopNew  bool
	runCount int64
}

type simJob struct {
	id, scaleSet, repo, name string
	runID                    int64
	queuedAt, startedAt      time.Time
	duration                 time.Duration
	runner, envID            string
	seq                      map[string]int64
	step                     int
}

var repos = []string{"octo/web-app", "octo/api-service", "octo/infra", "octo/mobile"}
var jobNames = []string{"build", "test (node 22)", "lint", "e2e (chromium)", "docker image", "deploy preview"}

// New builds a demo control plane under opts.DataDir.
func New(ctx context.Context, opts Options) (*Demo, error) {
	if opts.Tick <= 0 {
		opts.Tick = 500 * time.Millisecond
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 20 * time.Second
	}
	if opts.JobSeconds == [2]float64{} {
		opts.JobSeconds = [2]float64{8, 40}
	}
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, filepath.Join(opts.DataDir, "demo.db"))
	if err != nil {
		return nil, err
	}
	cfg := &config.Config{
		DataDir:  opts.DataDir,
		Capacity: config.Capacity{MaxEnvironments: 6, MemoryBudgetMB: 24576, MemoryMarginMB: 2048, MaxDiskPercent: 85},
		ScaleSets: []config.ScaleSet{
			{Name: "homelab-linux", URL: "https://github.com/octo", Credential: "demo", RunnerGroup: "default", MaxConcurrent: 4, Cores: 2, MemoryMB: 4096},
			{Name: "homelab-docker", URL: "https://github.com/octo/infra", Credential: "demo", RunnerGroup: "default", MaxConcurrent: 2, Cores: 4, MemoryMB: 8192},
		},
		Ingest: config.Ingest{Listen: "127.0.0.1:0", AdvertiseURL: "https://127.0.0.1:8443"},
	}
	rt := runtimetest.NewFake()
	rt.Cap = runtime.Capacity{HostMemoryTotalMB: 40960, HostMemoryAvailableMB: 30000, ThinPoolPercent: 42}
	d := &Demo{
		Store: db, Recorder: events.NewRecorder(db, events.NewBus(), nil), Logs: logs.New(filepath.Join(opts.DataDir, "logs"), db),
		Runtime: rt, Config: cfg, opts: opts, rng: rand.New(rand.NewSource(opts.Seed)),
		scalers: map[string]listener.Scaler{}, queued: map[string][]simJob{}, running: map[string]*simJob{}, booted: map[string]time.Time{},
	}
	timeouts := environment.DefaultTimeouts()
	timeouts[environment.Idle] = opts.IdleTimeout
	d.Controller = controller.New(controller.Deps{Store: db, Recorder: d.Recorder, Runtime: rt, GitHub: (*simGitHub)(d), Logs: d.Logs,
		Config: cfg, IngestURL: cfg.Ingest.AdvertiseURL, IngestFingerprint: "DE:MO", Timeouts: timeouts})
	for i, ss := range cfg.ScaleSets {
		d.Controller.SetScaleSetID(ss.Name, i+1)
		d.Controller.SetListening(ss.Name, true, nil)
		d.scalers[ss.Name] = d.Controller.Scaler(ss.Name)
	}
	return d, nil
}

// Close releases the store.
func (d *Demo) Close() error { return d.Store.Close() }

// StopNewJobs stops queueing new jobs; running ones finish.
func (d *Demo) StopNewJobs() {
	d.mu.Lock()
	d.stopNew = true
	d.mu.Unlock()
}

// Run drives the simulation and the controller until ctx ends.
func (d *Demo) Run(ctx context.Context) {
	go d.Controller.Run(ctx)
	t := time.NewTicker(d.opts.Tick)
	defer t.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.step(ctx)
			if n%10 == 0 {
				d.Controller.Reap(ctx)
			}
		}
	}
}

func (d *Demo) step(ctx context.Context) {
	now := time.Now()
	d.mu.Lock()
	if !d.stopNew && d.rng.Float64() < 0.25 {
		ss := d.Config.ScaleSets[d.rng.Intn(len(d.Config.ScaleSets))].Name
		d.runCount++
		min, max := d.opts.JobSeconds[0], d.opts.JobSeconds[1]
		d.queued[ss] = append(d.queued[ss], simJob{
			id: fmt.Sprintf("demo-%d-%s", d.runCount, ids.NewEnvironmentID()[20:]), scaleSet: ss,
			repo: repos[d.rng.Intn(len(repos))], name: jobNames[d.rng.Intn(len(jobNames))], runID: 9000 + d.runCount,
			queuedAt: now, duration: time.Duration((min + d.rng.Float64()*(max-min)) * float64(time.Second)),
		})
	}
	desired := map[string]int{}
	for _, ss := range d.Config.ScaleSets {
		desired[ss.Name] = len(d.queued[ss.Name])
	}
	for _, j := range d.running {
		desired[j.scaleSet]++
	}
	d.mu.Unlock()

	for name, n := range desired {
		_, _ = d.scalers[name].HandleDesiredRunnerCount(ctx, n)
	}
	envs, err := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: []string{"booting", "connected", "idle", "running"}})
	if err != nil {
		return
	}
	for _, e := range envs {
		d.advance(ctx, e, now)
	}
}

// advance plays the agent's part for one environment.
func (d *Demo) advance(ctx context.Context, e store.Environment, now time.Time) {
	d.mu.Lock()
	first, ok := d.booted[e.ID]
	if !ok {
		d.booted[e.ID] = now
		first = now
	}
	job := d.running[e.ID]
	d.mu.Unlock()
	age := now.Sub(first)

	switch e.State {
	case "booting":
		if age > 2*d.opts.Tick {
			d.agentLine(ctx, e.ID, "ghrm-agent demo started")
			d.Controller.AgentEvent(ctx, e.ID, ingest.EventHello, now, map[string]any{"ip": fmt.Sprintf("10.50.0.%d", 100+d.rng.Intn(100)), "version": "demo"})
			d.Controller.AgentEvent(ctx, e.ID, ingest.EventRunnerStarted, now, nil)
		}
	case "connected":
		d.runnerLine(ctx, e.ID, "√ Connected to GitHub")
		d.runnerLine(ctx, e.ID, "Listening for Jobs")
		d.Controller.AgentEvent(ctx, e.ID, ingest.EventRunnerOnline, now, nil)
	case "idle":
		d.mu.Lock()
		q := d.queued[e.ScaleSet]
		if len(q) == 0 {
			d.mu.Unlock()
			return
		}
		j := q[0]
		d.queued[e.ScaleSet] = q[1:]
		j.startedAt, j.runner, j.envID, j.seq = now, e.RunnerName, e.ID, map[string]int64{}
		d.running[e.ID] = &j
		d.mu.Unlock()
		owner, repo := splitRepo(j.repo)
		_ = d.scalers[e.ScaleSet].HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: e.RunnerName, JobMessageBase: scaleset.JobMessageBase{
			JobID: j.id, RepositoryName: repo, OwnerName: owner, JobDisplayName: j.name, WorkflowRunID: j.runID,
			JobWorkflowRef: j.repo + "/.github/workflows/ci.yml@refs/heads/main", EventName: "push", QueueTime: j.queuedAt, RunnerAssignTime: now}})
		d.runnerLine(ctx, e.ID, "Running job: "+j.name)
		d.Controller.AgentEvent(ctx, e.ID, ingest.EventJobStarted, now, map[string]any{"job": j.name})
	case "running":
		if job == nil {
			return
		}
		d.jobOutput(ctx, job, now)
		if now.Sub(job.startedAt) >= job.duration {
			d.finish(ctx, e, job, now)
		}
	}
}

var stepsScript = []string{
	"\x1b[36;1m##[group]Run actions/checkout@v5\x1b[0m", "Syncing repository: %s", "##[endgroup]",
	"\x1b[36;1m##[group]Run actions/setup-node@v5\x1b[0m", "Found in cache @ /home/runner/_work/_tool/node/22.23.3/x64", "##[endgroup]",
	"\x1b[36;1m##[group]Run npm ci\x1b[0m", "added 812 packages in 9s", "##[endgroup]",
	"\x1b[36;1m##[group]Run npm test\x1b[0m", "\x1b[32m✓\x1b[0m renders the dashboard (42 ms)", "\x1b[32m✓\x1b[0m streams events (18 ms)",
	"\x1b[33m⚠\x1b[0m slow test: e2e login (1.9 s)", "Tests: 128 passed, 128 total", "##[endgroup]",
}

func (d *Demo) jobOutput(ctx context.Context, j *simJob, now time.Time) {
	if j.step < len(stepsScript) {
		line := stepsScript[j.step]
		if j.step == 1 {
			line = fmt.Sprintf(line, j.repo)
		}
		d.appendLine(ctx, j, "job", now.UTC().Format(time.RFC3339Nano)+" "+line)
		j.step++
	}
	d.appendLine(ctx, j, "runner", fmt.Sprintf("[%s INFO JobRunner] heartbeat for %s", now.UTC().Format("2006-01-02 15:04:05Z"), j.name))
	d.mu.Lock()
	j.seq["metrics"]++
	seq := j.seq["metrics"]
	cpu := int64(float64(now.Sub(j.startedAt).Microseconds()) * (0.6 + d.rng.Float64()))
	mem := int64((600 + d.rng.Intn(1400)) << 20)
	d.mu.Unlock()
	_, _ = d.Logs.Append(ctx, j.envID, "metrics", []logs.Line{{Seq: seq, Time: now, Text: fmt.Sprintf(`{"cpu_usec":%d,"mem_bytes":%d}`, cpu, mem)}})
}

func (d *Demo) appendLine(ctx context.Context, j *simJob, stream, text string) {
	d.mu.Lock()
	j.seq[stream]++
	seq := j.seq[stream]
	d.mu.Unlock()
	_, _ = d.Logs.Append(ctx, j.envID, stream, []logs.Line{{Seq: seq, Time: time.Now(), Text: text}})
}

func (d *Demo) finish(ctx context.Context, e store.Environment, j *simJob, now time.Time) {
	result, code := "succeeded", 0
	if d.rng.Float64() < 0.15 {
		result, code = "failed", 1
		d.appendLine(ctx, j, "job", now.UTC().Format(time.RFC3339Nano)+" \x1b[31m##[error]Process completed with exit code 1.\x1b[0m")
	}
	d.runnerLine(ctx, e.ID, fmt.Sprintf("Job %s completed with result: %s", j.name, map[string]string{"succeeded": "Succeeded", "failed": "Failed"}[result]))
	owner, repo := splitRepo(j.repo)
	_ = d.scalers[e.ScaleSet].HandleJobCompleted(ctx, &scaleset.JobCompleted{Result: result, RunnerName: e.RunnerName, JobMessageBase: scaleset.JobMessageBase{
		JobID: j.id, RepositoryName: repo, OwnerName: owner, JobDisplayName: j.name, WorkflowRunID: j.runID, FinishTime: now}})
	d.Controller.AgentEvent(ctx, e.ID, ingest.EventJobFinished, now, map[string]any{"job": j.name, "result": result})
	d.Controller.AgentEvent(ctx, e.ID, ingest.EventRunnerExited, now, map[string]any{"exit_code": float64(code)})
	d.Controller.AgentEvent(ctx, e.ID, ingest.EventShutdown, now, nil)
	d.mu.Lock()
	delete(d.running, e.ID)
	delete(d.booted, e.ID)
	d.mu.Unlock()
	_ = d.Runtime.Stop(ctx, runtime.Ref{ID: e.RuntimeRef}) // the guest powers itself off
	d.Controller.Kick()
}

func (d *Demo) agentLine(ctx context.Context, envID, text string) {
	_ = d.Logs.Write(ctx, envID, "agent", text, time.Now())
}

func (d *Demo) runnerLine(ctx context.Context, envID, text string) {
	_ = d.Logs.Write(ctx, envID, "runner", time.Now().UTC().Format("2006-01-02 15:04:05Z")+": "+text, time.Now())
}

func splitRepo(full string) (owner, repo string) {
	for i := range full {
		if full[i] == '/' {
			return full[:i], full[i+1:]
		}
	}
	return "", full
}

// simGitHub satisfies controller.GitHub for the demo.
type simGitHub Demo

func (s *simGitHub) EnsureScaleSet(context.Context, config.ScaleSet) (int, error) { return 1, nil }

func (s *simGitHub) GenerateJIT(_ context.Context, _ string, _ int, runnerName string) (int64, string, error) {
	d := (*Demo)(s)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.runCount++
	return d.runCount, "demo-jit-" + runnerName, nil
}

func (s *simGitHub) RemoveRunner(context.Context, string, int64) error { return nil }

func (s *simGitHub) Listen(ctx context.Context, _ string, _, _ int, _ listener.Scaler) error {
	<-ctx.Done()
	return ctx.Err()
}
