package controller

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/runtime/runtimetest"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeGitHub struct {
	mu      sync.Mutex
	next    int64
	jits    []string
	removed []int64
	onJIT   func(runnerName string)
}

func (f *fakeGitHub) EnsureScaleSet(context.Context, config.ScaleSet) (int, error) { return 7, nil }
func (f *fakeGitHub) GenerateJIT(_ context.Context, _ string, _ int, runnerName string) (int64, string, error) {
	f.mu.Lock()
	f.next++
	id := f.next
	f.jits = append(f.jits, runnerName)
	hook := f.onJIT
	f.mu.Unlock()
	if hook != nil {
		hook(runnerName)
	}
	return id, "jit-" + runnerName, nil
}
func (f *fakeGitHub) RemoveRunner(_ context.Context, _ string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}
func (f *fakeGitHub) Listen(ctx context.Context, _ string, _, _ int, _ listener.Scaler) error {
	<-ctx.Done()
	return ctx.Err()
}

type harness struct {
	c   *Controller
	db  *store.Store
	rt  *runtimetest.Fake
	gh  *fakeGitHub
	cfg *config.Config
	now time.Time
}

func newHarness(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		Capacity:  config.Capacity{MaxEnvironments: 10, MemoryBudgetMB: 64 << 10, MemoryMarginMB: 1024, MaxDiskPercent: 85},
		ScaleSets: []config.ScaleSet{{Name: "lab", URL: "https://github.com/o/r", Credential: "c", MaxConcurrent: 4, Cores: 1, MemoryMB: 512}},
	}
	if mutate != nil {
		mutate(cfg)
	}
	rt := runtimetest.NewFake()
	rt.Cap = runtime.Capacity{HostMemoryTotalMB: 64 << 10, HostMemoryAvailableMB: 60 << 10}
	h := &harness{db: db, rt: rt, gh: &fakeGitHub{}, cfg: cfg, now: time.Now()}
	h.c = New(Deps{
		Store: db, Recorder: events.NewRecorder(db, events.NewBus(), nil), Runtime: rt, GitHub: h.gh,
		Logs: logs.New(t.TempDir(), db), Config: cfg,
		IngestURL: "https://10.50.0.2:8443", IngestFingerprint: "AA:BB",
		Now: func() time.Time { return h.now },
	})
	h.c.SetScaleSetID("lab", 7)
	return h
}

func (h *harness) envs(t *testing.T, states ...string) []store.Environment {
	t.Helper()
	es, err := h.db.ListEnvironments(context.Background(), store.EnvironmentFilter{States: states})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func (h *harness) provision(t *testing.T, n int) []store.Environment {
	t.Helper()
	if _, err := h.c.Scaler("lab").HandleDesiredRunnerCount(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	h.c.Wait()
	return h.envs(t, "booting")
}

func TestDesiredCountProvisionsEnvironments(t *testing.T) {
	h := newHarness(t, nil)
	es := h.provision(t, 2)
	if len(es) != 2 {
		t.Fatalf("booting environments = %d, want 2", len(es))
	}
	list, _ := h.rt.List(context.Background())
	if len(list) != 2 || !list[0].Running || !list[1].Running {
		t.Fatalf("runtime = %+v, want 2 running guests", list)
	}
	hashes := map[string]bool{}
	for _, e := range es {
		spec, ok := h.rt.Spec(runtime.Ref{ID: e.RuntimeRef})
		if !ok {
			t.Fatalf("no runtime guest for %s (ref %q)", e.ID, e.RuntimeRef)
		}
		for _, k := range []string{ingest.EnvJITConfig, ingest.EnvEnvironment, ingest.EnvURL, ingest.EnvToken, ingest.EnvFingerprint} {
			if spec.Env[k] == "" {
				t.Errorf("env %s missing in %s", k, e.ID)
			}
		}
		if spec.Env[ingest.EnvJITConfig] != "jit-"+e.RunnerName || e.RunnerID == 0 {
			t.Errorf("JIT/runner mismatch: %+v", e)
		}
		if ingest.HashToken(spec.Env[ingest.EnvToken]) != e.TokenHash {
			t.Errorf("token hash mismatch for %s", e.ID)
		}
		hashes[e.TokenHash] = true
		if got, ok := h.c.Resolve(context.Background(), e.TokenHash); !ok || got != e.ID {
			t.Errorf("Resolve(%s) = %q %v", e.ID, got, ok)
		}
	}
	if len(hashes) != 2 {
		t.Fatal("tokens must be unique per environment")
	}
	n, _ := h.c.Scaler("lab").HandleDesiredRunnerCount(context.Background(), 2)
	h.c.Wait()
	if n != 2 || len(h.envs(t)) != 2 {
		t.Fatalf("a repeated desired count must not create more environments (count %d, envs %d)", n, len(h.envs(t)))
	}
}

func TestCapacityLimitsProvisioning(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.Capacity.MaxEnvironments = 1 })
	if es := h.provision(t, 5); len(es) != 1 {
		t.Fatalf("environments = %d, want 1", len(es))
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	found := false
	for _, e := range evs {
		if e.Kind == "scaleset.waiting" && e.Data["reason"] == "global_limit" {
			found = true
		}
	}
	if !found {
		t.Fatal("want a scaleset.waiting event with reason global_limit")
	}
}

func TestProvisionFailureIsCleanedUp(t *testing.T) {
	h := newHarness(t, nil)
	h.rt.CreateErr = errors.New("boom")
	h.provision(t, 1)
	es := h.envs(t, "destroyed")
	if len(es) != 1 || es[0].FailureStage != "create" || es[0].FailureReason == "" {
		t.Fatalf("destroyed = %+v, want one with failure stage create", es)
	}
	if len(h.gh.removed) != 1 || h.gh.removed[0] != es[0].RunnerID {
		t.Fatalf("removed runners = %v, want the JIT runner %d", h.gh.removed, es[0].RunnerID)
	}
}

func TestAgentEventsDriveStates(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	steps := []struct {
		event string
		data  map[string]any
		want  string
	}{
		{ingest.EventHello, map[string]any{"ip": "10.50.0.101"}, "connected"},
		{ingest.EventRunnerOnline, nil, "idle"},
		{ingest.EventJobStarted, nil, "running"},
		{ingest.EventRunnerExited, map[string]any{"exit_code": 0.0}, "completing"},
	}
	for _, s := range steps {
		h.c.AgentEvent(ctx, e.ID, s.event, time.Now(), s.data)
		got, _ := h.db.GetEnvironment(ctx, e.ID)
		if got.State != s.want {
			t.Fatalf("after %s state = %s, want %s", s.event, got.State, s.want)
		}
	}
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.IP != "10.50.0.101" || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("env = %+v", got)
	}
	h.c.AgentEvent(ctx, e.ID, ingest.EventHello, time.Now(), nil) // late duplicate: ignored, no panic
	h.c.AgentEvent(ctx, "unknown-env", ingest.EventHello, time.Now(), nil)
}

func TestRunnerExitedStraightFromBooting(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	h.c.AgentEvent(context.Background(), e.ID, ingest.EventRunnerExited, time.Now(), map[string]any{"exit_code": 1.0})
	got, _ := h.db.GetEnvironment(context.Background(), e.ID)
	if got.State != "completing" {
		t.Fatalf("state = %s, want completing (walking through the intermediate states)", got.State)
	}
}

func jobBase(id, runner string) scaleset.JobMessageBase {
	return scaleset.JobMessageBase{JobID: id, RepositoryName: "r", OwnerName: "o", JobDisplayName: "build", WorkflowRunID: 99, QueueTime: time.Now()}
}

func TestJobMessagesUpsertJobsAndTolerateUnknownRunners(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	h.c.AgentEvent(ctx, e.ID, ingest.EventHello, time.Now(), nil)
	h.c.AgentEvent(ctx, e.ID, ingest.EventRunnerOnline, time.Now(), nil)
	sc := h.c.Scaler("lab")
	if err := sc.HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: e.RunnerName, JobMessageBase: jobBase("j1", e.RunnerName)}); err != nil {
		t.Fatal(err)
	}
	j, err := h.db.GetJob(ctx, "j1")
	if err != nil || j.EnvironmentID != e.ID || j.Repository != "o/r" || j.Status != "running" || j.RunID != 99 {
		t.Fatalf("job = %+v, %v", j, err)
	}
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "running" || got.JobID != "j1" {
		t.Fatalf("env = %+v", got)
	}
	if err := sc.HandleJobCompleted(ctx, &scaleset.JobCompleted{Result: "succeeded", RunnerName: e.RunnerName, JobMessageBase: jobBase("j1", e.RunnerName)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "completing" {
		t.Fatalf("env state = %s, want completing", got.State)
	}
	if err := sc.HandleJobCompleted(ctx, &scaleset.JobCompleted{Result: "canceled", RunnerName: "ghost", JobMessageBase: jobBase("j2", "ghost")}); err != nil {
		t.Fatalf("unknown runner = %v, want nil", err)
	}
	if j2, err := h.db.GetJob(ctx, "j2"); err != nil || j2.Result != "canceled" {
		t.Fatalf("j2 = %+v, %v", j2, err)
	}
}

func TestCompletingEnvironmentIsDestroyed(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	h.c.AgentEvent(ctx, e.ID, ingest.EventRunnerExited, time.Now(), map[string]any{"exit_code": 0.0})
	_ = h.rt.Stop(ctx, runtime.Ref{ID: e.RuntimeRef}) // the guest powered itself off
	h.c.Teardown(ctx)
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.State != "destroyed" {
		t.Fatalf("state = %s, want destroyed", got.State)
	}
	if list, _ := h.rt.List(ctx); len(list) != 0 {
		t.Fatalf("runtime still has %v", list)
	}
}

func TestKeepOnFailureDelaysDestroy(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.ScaleSets[0].KeepOnFailureMinutes = 5 })
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.c.Fail(ctx, e.ID, "test", errors.New("job failed badly"))
	h.c.Teardown(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "failed" {
		t.Fatalf("state = %s, want failed (kept for debugging)", got.State)
	}
	h.now = h.now.Add(6 * time.Minute)
	h.c.Teardown(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "destroyed" {
		t.Fatalf("state = %s, want destroyed after the keep window", got.State)
	}
}

func TestStartupAdoptsLiveEnvironments(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	ref, _ := h.rt.Create(ctx, runtime.EnvironmentSpec{ID: "existing", Hostname: "ghrm-existing", Cores: 1, MemoryMB: 512})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "existing", ScaleSet: "lab", State: "running", RuntimeRef: ref.ID, MemoryMB: 512})
	n, err := h.c.Scaler("lab").HandleDesiredRunnerCount(ctx, 1)
	h.c.Wait()
	if err != nil || n != 1 || len(h.envs(t)) != 1 {
		t.Fatalf("count %d err %v envs %d; want the running environment adopted, nothing new", n, err, len(h.envs(t)))
	}
}

func TestReaperDestroysOrphans(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	orphan, _ := h.rt.Create(ctx, runtime.EnvironmentSpec{ID: "orphan", Hostname: "ghrm-orphan", Cores: 1, MemoryMB: 512})
	gone, _ := h.rt.Create(ctx, runtime.EnvironmentSpec{ID: "gone", Hostname: "ghrm-gone", Cores: 1, MemoryMB: 512})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "gone", ScaleSet: "lab", State: "destroyed", RuntimeRef: gone.ID})
	h.c.Reap(ctx)
	if list, _ := h.rt.List(ctx); len(list) != 0 {
		t.Fatalf("runtime still has %+v; want orphan %s and destroyed-row guest removed", list, orphan)
	}
}

func TestReaperMarksGoneEnvironments(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	_ = h.rt.Destroy(ctx, runtime.Ref{ID: e.RuntimeRef}) // vanished outside ghrm
	h.c.Reap(ctx)
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.State != "destroyed" || got.FailureStage != "runtime_gone" {
		t.Fatalf("env = %s / %s, want destroyed with failure stage runtime_gone", got.State, got.FailureStage)
	}
}

func TestReaperEnforcesTimeouts(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0] // booting, guest running, agent never says hello
	h.now = h.now.Add(3 * time.Minute)
	h.c.Reap(ctx)
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.State != "destroyed" || got.FailureStage != "timeout:booting" {
		t.Fatalf("env = %s / %s, want destroyed after timeout:booting", got.State, got.FailureStage)
	}
	if list, _ := h.rt.List(ctx); len(list) != 0 {
		t.Fatalf("guest not destroyed: %+v", list)
	}
}

func TestReaperHandlesSilentPowerOff(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.c.AgentEvent(ctx, e.ID, ingest.EventHello, time.Now(), nil)
	h.c.AgentEvent(ctx, e.ID, ingest.EventRunnerOnline, time.Now(), nil)
	_ = h.rt.Stop(ctx, runtime.Ref{ID: e.RuntimeRef}) // powered off without reporting
	h.now = h.now.Add(61 * time.Second)
	h.c.Reap(ctx)
	h.c.Teardown(ctx)
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.State != "destroyed" {
		t.Fatalf("state = %s, want destroyed", got.State)
	}
}

func TestRequestDestroy(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	if err := h.c.RequestDestroy(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "destroyed" {
		t.Fatalf("state = %s", got.State)
	}
	if err := h.c.RequestDestroy(ctx, e.ID); err == nil {
		t.Fatal("destroying a destroyed environment must fail")
	}
	if err := h.c.RequestDestroy(ctx, "missing"); err == nil {
		t.Fatal("unknown environment must fail")
	}
}

// Right after a start the hypervisor's cached listing can still say "stopped";
// the reaper must confirm with a live status before concluding the guest powered off.
func TestReaperIgnoresStaleListStatus(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.rt.StaleList = true
	h.now = h.now.Add(90 * time.Second)
	h.c.Reap(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "booting" {
		t.Fatalf("state = %s, want booting (the guest is running)", got.State)
	}
}

func TestReaperWaitsBeforeConcludingSilentPowerOff(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	_ = h.rt.Stop(ctx, runtime.Ref{ID: e.RuntimeRef})
	h.c.Reap(ctx) // just entered booting: too early to conclude anything
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "booting" {
		t.Fatalf("state = %s, want booting within the grace period", got.State)
	}
	h.now = h.now.Add(61 * time.Second)
	h.c.Reap(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "completing" && got.State != "destroyed" {
		t.Fatalf("state = %s, want completing after the grace period", got.State)
	}
}

// Final review Important #1: a finished job must not trigger another environment
// before the lower demand arrives from GitHub.
func TestFinishedJobDoesNotProvisionAnExtraEnvironment(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	for _, ev := range []string{ingest.EventHello, ingest.EventRunnerOnline, ingest.EventJobStarted, ingest.EventRunnerExited} {
		h.c.AgentEvent(ctx, e.ID, ev, time.Now(), nil)
	}
	if err := h.c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	h.c.Wait()
	if n := len(h.envs(t)); n != 1 {
		t.Fatalf("environments = %d, want 1 (no extra environment for a finished job)", n)
	}
}

// Final review Important #6: an environment created after the runtime snapshot must not be failed as gone.
func TestReaperConfirmsBeforeDeclaringRuntimeGone(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.rt.ListExclude = map[string]bool{e.ID: true}
	h.c.Reap(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "booting" {
		t.Fatalf("state = %s / %s, want booting", got.State, got.FailureStage)
	}
}

func TestCompletingWaitsForPowerOff(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.c.AgentEvent(ctx, e.ID, ingest.EventRunnerExited, time.Now(), nil)
	h.now = h.now.Add(45 * time.Second) // the agent is still flushing logs
	h.c.Teardown(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "completing" {
		t.Fatalf("state = %s, want completing while the guest still runs", got.State)
	}
	_ = h.rt.Stop(ctx, runtime.Ref{ID: e.RuntimeRef})
	h.c.Teardown(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "destroyed" {
		t.Fatalf("state = %s, want destroyed once the guest powered off", got.State)
	}
}

func TestIdleTimeoutIsNotAFailure(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.ScaleSets[0].KeepOnFailureMinutes = 30 })
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.c.AgentEvent(ctx, e.ID, ingest.EventHello, time.Now(), nil)
	h.c.AgentEvent(ctx, e.ID, ingest.EventRunnerOnline, time.Now(), nil)
	h.now = h.now.Add(11 * time.Minute)
	h.c.Reap(ctx)
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.State != "destroyed" || got.FailureStage != "" {
		t.Fatalf("env = %s / %q, want destroyed without a failure (scale-down is normal)", got.State, got.FailureStage)
	}
}

func TestDestroyRetriesAreThrottled(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.rt.DestroyErr = errors.New("locked")
	_ = h.c.RequestDestroy(ctx, e.ID)
	for range 5 {
		h.c.Teardown(ctx)
	}
	evs, _ := h.db.ListEvents(ctx, store.EventFilter{EnvironmentID: e.ID})
	warns := 0
	for _, ev := range evs {
		if ev.Kind == "environment.destroy_failed" {
			warns++
		}
	}
	if warns != 1 || h.rt.DestroyCalls != 1 {
		t.Fatalf("destroy_failed events = %d, destroy calls = %d; want 1 and 1 within the backoff", warns, h.rt.DestroyCalls)
	}
	h.rt.DestroyErr = nil
	h.now = h.now.Add(time.Minute)
	h.c.Teardown(ctx)
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State != "destroyed" {
		t.Fatalf("state = %s, want destroyed after the backoff", got.State)
	}
}

func TestJITRunnerRemovedWhenEnvironmentDestroyedMeanwhile(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.gh.onJIT = func(runnerName string) {
		e, _ := h.db.FindEnvironmentByRunner(ctx, runnerName)
		_ = h.c.RequestDestroy(ctx, e.ID) // an operator destroys it while the JIT config is generated
	}
	h.provision(t, 1)
	if len(h.gh.removed) != 1 {
		t.Fatalf("removed runners = %v, want the JIT registration removed", h.gh.removed)
	}
	if list, _ := h.rt.List(ctx); len(list) != 0 {
		t.Fatalf("a destroyed environment was still created: %+v", list)
	}
}

func TestConcurrentDestroysRunOnce(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.rt.DestroyDelay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h.c.RequestDestroy(ctx, e.ID) }()
	}
	wg.Wait()
	if h.rt.DestroyCalls != 1 {
		t.Fatalf("runtime destroy calls = %d, want 1", h.rt.DestroyCalls)
	}
}

func TestJobTimesUseTheControllerClock(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.now = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	sc := h.c.Scaler("lab")
	_ = sc.HandleJobCompleted(ctx, &scaleset.JobCompleted{Result: "succeeded", RunnerName: "x", JobMessageBase: scaleset.JobMessageBase{JobID: "j9"}})
	evs, _ := h.db.ListEvents(ctx, store.EventFilter{JobID: "j9"})
	j, _ := h.db.GetJob(ctx, "j9")
	if !j.FinishedAt.Equal(h.now) || len(evs) != 1 || !evs[0].Time.Equal(h.now) {
		t.Fatalf("job finished %v, event %v; want the controller clock %v", j.FinishedAt, evs, h.now)
	}
}

// GitHub sends the queue time only with the "available" message; the started and
// completed messages carry a zero queue time and must not erase it.
func TestAvailableJobRecordsTheQueueTime(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	sc := h.c.Scaler("lab").(interface {
		HandleJobAvailable(context.Context, *scaleset.JobAvailable) error
	})
	queued := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	base := scaleset.JobMessageBase{JobID: "j1", RepositoryName: "r", OwnerName: "o", JobDisplayName: "build", WorkflowRunID: 99, QueueTime: queued}
	if err := sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: base}); err != nil {
		t.Fatal(err)
	}
	j, err := h.db.GetJob(ctx, "j1")
	if err != nil || j.Status != "assigned" || !j.QueuedAt.Equal(queued) || j.Repository != "o/r" {
		t.Fatalf("job = %+v, %v; want assigned, queued at %v", j, err, queued)
	}
	started := base
	started.QueueTime = time.Time{}
	_ = h.c.Scaler("lab").HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: "x", JobMessageBase: started})
	// A repeated "available" message must not move a started job back to assigned.
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: base})
	if j, _ = h.db.GetJob(ctx, "j1"); j.Status != "running" || !j.QueuedAt.Equal(queued) {
		t.Fatalf("job = %+v; want running, still queued at %v", j, queued)
	}

	// Without a queue time, the scale set assignment time, then the controller clock, stand in.
	assigned := queued.Add(time.Second)
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: scaleset.JobMessageBase{JobID: "j2", ScaleSetAssignTime: assigned}})
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: scaleset.JobMessageBase{JobID: "j3"}})
	if j, _ = h.db.GetJob(ctx, "j2"); !j.QueuedAt.Equal(assigned) {
		t.Fatalf("j2 queued at %v, want %v", j.QueuedAt, assigned)
	}
	if j, _ = h.db.GetJob(ctx, "j3"); !j.QueuedAt.Equal(h.now.Truncate(time.Millisecond)) {
		t.Fatalf("j3 queued at %v, want the controller clock %v", j.QueuedAt, h.now)
	}
}

type stageLog struct {
	mu   sync.Mutex
	seen map[string]time.Duration
}

func (s *stageLog) ObserveStage(stage string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[stage] = d
}

func TestStageDurationsAreObserved(t *testing.T) {
	h := newHarness(t, nil)
	obs := &stageLog{seen: map[string]time.Duration{}}
	h.c.d.Stages = obs
	e := h.provision(t, 1)[0]
	h.now = h.now.Add(7 * time.Second)
	h.c.AgentEvent(context.Background(), e.ID, ingest.EventHello, time.Now(), nil)
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if d, ok := obs.seen["booting"]; !ok || d < 6*time.Second {
		t.Fatalf("observed %v; want booting lasting about 7s", obs.seen)
	}
}

// GitHub's started message has no queue time but has the scale set assignment time,
// which stands in when the assigned message was missed (a control plane restart).
func TestStartedJobWithoutAnEarlierMessageGetsTheAssignmentTime(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	assigned := time.Date(2026, 10, 7, 21, 11, 22, 0, time.UTC)
	base := scaleset.JobMessageBase{JobID: "j7", ScaleSetAssignTime: assigned, RunnerAssignTime: assigned.Add(20 * time.Second)}
	_ = h.c.Scaler("lab").HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: "x", JobMessageBase: base})
	if j, _ := h.db.GetJob(ctx, "j7"); !j.QueuedAt.Equal(assigned) {
		t.Fatalf("queued at %v, want the assignment time %v", j.QueuedAt, assigned)
	}
}

// A queue time already recorded (from an available or assigned message) is kept: the
// assignment time on later messages is only a stand-in for a missing one.
func TestLaterMessagesDoNotMoveTheQueueTime(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	sc := h.c.Scaler("lab").(interface {
		HandleJobAvailable(context.Context, *scaleset.JobAvailable) error
	})
	queued := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	assigned := queued.Add(30 * time.Second)
	_ = sc.HandleJobAvailable(ctx, &scaleset.JobAvailable{JobMessageBase: scaleset.JobMessageBase{JobID: "j8", QueueTime: queued}})
	later := scaleset.JobMessageBase{JobID: "j8", ScaleSetAssignTime: assigned, RunnerAssignTime: assigned.Add(time.Minute)}
	_ = h.c.Scaler("lab").HandleJobStarted(ctx, &scaleset.JobStarted{RunnerName: "x", JobMessageBase: later})
	_ = h.c.Scaler("lab").HandleJobCompleted(ctx, &scaleset.JobCompleted{Result: "succeeded", RunnerName: "x", JobMessageBase: later})
	if j, _ := h.db.GetJob(ctx, "j8"); !j.QueuedAt.Equal(queued) {
		t.Fatalf("queued at %v, want the recorded %v", j.QueuedAt, queued)
	}
}
