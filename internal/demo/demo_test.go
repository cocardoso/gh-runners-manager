package demo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func TestDemoRunsJobsThroughTheWholeLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Options{DataDir: filepath.Join(t.TempDir(), "demo"), Seed: 7, Tick: 5 * time.Millisecond, JobSeconds: [2]float64{0.05, 0.15}, IdleTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	go d.Run(ctx)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := d.Store.ListJobs(ctx, store.JobFilter{Status: "completed"})
		if len(jobs) >= 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.StopNewJobs()
	for time.Now().Before(deadline) {
		live, _ := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: []string{"pending", "provisioning", "booting", "connected", "idle", "running", "completing", "failed", "destroying"}})
		if len(live) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	jobs, _ := d.Store.ListJobs(ctx, store.JobFilter{Status: "completed"})
	if len(jobs) < 6 {
		t.Fatalf("completed jobs = %d, want at least 6", len(jobs))
	}
	live, _ := d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: []string{"pending", "provisioning", "booting", "connected", "idle", "running", "completing", "failed", "destroying"}})
	if len(live) != 0 {
		t.Fatalf("live environments left behind: %d", len(live))
	}
	evs, _ := d.Store.ListEvents(ctx, store.EventFilter{Limit: 5000})
	kinds := map[string]bool{}
	for _, e := range evs {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"environment.created", "environment.state", "job.started", "job.completed", "agent.hello", "agent.runner_online", "agent.job_started", "agent.runner_exited"} {
		if !kinds[k] {
			t.Errorf("no %s event", k)
		}
	}
	// The newest environment may have expired idle without a job: check one that ran a job.
	ranJob := jobs[0].EnvironmentID
	streams, _ := d.Store.ListLogStreams(ctx, ranJob)
	names := map[string]bool{}
	for _, s := range streams {
		names[s.Stream] = true
	}
	for _, s := range []string{"control-plane", "runtime", "agent", "runner", "job", "metrics"} {
		if !names[s] {
			t.Errorf("environment %s has no %s log", ranJob, s)
		}
	}
}

func TestDemoKeepsTheQueueBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Jobs that never finish: without a bound the simulated queue would grow forever.
	d, err := New(ctx, Options{DataDir: filepath.Join(t.TempDir(), "demo"), Seed: 3, Tick: time.Millisecond, JobSeconds: [2]float64{3600, 3600}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 400; i++ {
		d.step(ctx)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for ss, q := range d.queued {
		if len(q) > maxQueuedPerScaleSet {
			t.Fatalf("%s has %d queued jobs, want at most %d", ss, len(q), maxQueuedPerScaleSet)
		}
	}
}

func TestDemoBuildsATemplateEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := New(ctx, Options{DataDir: filepath.Join(t.TempDir(), "demo"), Seed: 5, Tick: 5 * time.Millisecond, JobSeconds: [2]float64{0.05, 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	go d.Run(ctx)
	tpl, err := d.Templates.Build(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := d.Store.GetTemplate(ctx, tpl.ID); got.State == store.TemplateActive || got.State == store.TemplateFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := d.Store.GetTemplate(ctx, tpl.ID)
	if got.State != store.TemplateActive {
		t.Fatalf("template = %+v, want active", got)
	}
	streams, _ := d.Store.ListLogStreams(ctx, got.BuildEnvID)
	names := map[string]bool{}
	for _, s := range streams {
		names[s.Stream] = true
	}
	if !names["build"] {
		t.Fatalf("builder streams = %v, want a build log", names)
	}
	if ref, _ := d.Templates.Active(ctx); ref == "" {
		t.Fatal("the new template must be active for new environments")
	}
}
