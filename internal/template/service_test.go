package template

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/runtime/runtimetest"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeEnvs struct {
	mu        sync.Mutex
	db        *store.Store
	rt        *runtimetest.Fake
	n         int
	started   []controller.SpecialSpec
	ids       []string
	destroyed []string
	refs      map[string]runtime.Ref
	failStart bool
}

func (f *fakeEnvs) StartSpecial(ctx context.Context, s controller.SpecialSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStart {
		return "", errors.New("clone failed")
	}
	f.n++
	id := s.Kind + string(rune('0'+f.n))
	f.started = append(f.started, s)
	f.ids = append(f.ids, id)
	ref, err := f.rt.Create(ctx, runtime.EnvironmentSpec{ID: strings.ToLower(id), Hostname: "h", Cores: 1, MemoryMB: 512, Template: s.Template})
	if err != nil {
		return "", err
	}
	f.refs[id] = ref
	return id, f.db.CreateEnvironment(ctx, store.Environment{ID: id, Kind: s.Kind, State: "booting", RuntimeRef: ref.ID})
}

func (f *fakeEnvs) RequestDestroy(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed = append(f.destroyed, id)
	_ = f.rt.Destroy(ctx, f.refs[id])
	_, _ = f.db.TransitionEnvironment(ctx, id, nil, "destroying", nil)
	_, err := f.db.TransitionEnvironment(ctx, id, nil, "destroyed", nil)
	return err
}

func (f *fakeEnvs) wasDestroyed(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.destroyed {
		if d == id {
			return true
		}
	}
	return false
}

type fakeReleases struct {
	slim   Release
	runner RunnerRelease
	report []byte
	err    error
}

func (f *fakeReleases) LatestSlim(context.Context) (Release, error)         { return f.slim, f.err }
func (f *fakeReleases) LatestRunner(context.Context) (RunnerRelease, error) { return f.runner, f.err }
func (f *fakeReleases) PublishedReport(context.Context, Release) ([]byte, error) {
	return f.report, f.err
}

type tharness struct {
	s    *Service
	db   *store.Store
	rt   *runtimetest.Fake
	envs *fakeEnvs
	rel  *fakeReleases
	now  time.Time
	dir  string
}

func newService(t *testing.T, mutate func(*config.Templates)) *tharness {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rt := runtimetest.NewFake()
	agentPath := filepath.Join(dir, "ghrm-agent")
	_ = os.WriteFile(agentPath, []byte("\x7fELF"), 0o755)
	cfg := config.Templates{VMIDRange: config.VMIDRange{Start: 950, End: 958}, Keep: 2, CheckInterval: config.Duration(24 * time.Hour), AutoActivate: true,
		BuildTimeout: config.Duration(90 * time.Minute), VerifyTimeout: config.Duration(20 * time.Minute), MaxArchiveBytes: 1 << 20,
		BuilderCores: 4, BuilderMemoryMB: 8192, BuilderDiskGB: 48, AgentPath: agentPath, SelfTestBlocked: []string{"10.1.1.1:443"}}
	if mutate != nil {
		mutate(&cfg)
	}
	h := &tharness{db: db, rt: rt, dir: dir, now: time.Now(),
		envs: &fakeEnvs{db: db, rt: rt, refs: map[string]runtime.Ref{}},
		rel: &fakeReleases{slim: Release{Tag: "ubuntu-slim/20261005.17", Version: "20261005.17"},
			runner: RunnerRelease{Version: "2.338.0", SHA256: strings.Repeat("c", 64)}, report: []byte(published)}}
	h.s = NewService(Deps{Store: db, Recorder: events.NewRecorder(db, events.NewBus(), nil), Logs: logs.New(filepath.Join(dir, "logs"), db),
		Runtime: rt, Environments: h.envs, Releases: h.rel, Config: cfg, BootstrapVMID: 949, DataDir: dir, Now: func() time.Time { return h.now }})
	if err := h.s.EnsureBootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

func archiveOf(b []byte) (io.Reader, string) {
	sum := sha256.Sum256(b)
	return bytes.NewReader(b), hex.EncodeToString(sum[:])
}

func (h *tharness) state(t *testing.T, id string) store.Template {
	t.Helper()
	tpl, err := h.db.GetTemplate(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

// buildToVerify runs a build up to the verify environment.
func (h *tharness) buildToVerify(t *testing.T) store.Template {
	t.Helper()
	ctx := context.Background()
	tpl, err := h.s.Build(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.upload(ctx, tpl.BuildEnvID, []byte("rootfs-"+tpl.ID)); err != nil {
		t.Fatal(err)
	}
	h.s.Wait()
	got := h.state(t, tpl.ID)
	if got.State != store.TemplateVerifying || got.VerifyEnvID == "" {
		t.Fatalf("after the archive = %+v", got)
	}
	return got
}

func (h *tharness) upload(ctx context.Context, envID string, b []byte) error {
	r, sum := archiveOf(b)
	return h.s.ReceiveRootFS(ctx, envID, r, sum)
}

func okReport(software string) ingest.SelfTestReport {
	return ingest.SelfTestReport{Checks: []ingest.Check{{Name: "docker hello-world", OK: true}}, Software: json.RawMessage(software)}
}

func TestBuildVerifyActivate(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	if ref, vmid := h.s.Active(ctx); ref != "" || vmid != 949 {
		t.Fatalf("bootstrap active = %q %d", ref, vmid)
	}
	tpl, err := h.s.Build(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.State != store.TemplateBuilding || tpl.SlimRelease != "20261005.17" || tpl.RunnerVersion != "2.338.0" || tpl.LayerVersion == "" {
		t.Fatalf("version = %+v", tpl)
	}
	b := h.envs.started[0]
	if b.Kind != store.KindBuild || b.Template != "" || b.DiskGB != 48 || b.Cores != 4 || b.Env[ingest.EnvMode] != ingest.ModeBuild {
		t.Fatalf("builder spec = %+v", b)
	}
	spec, err := h.s.BuildSpec(ctx, tpl.BuildEnvID)
	if err != nil || spec.SlimTag != "ubuntu-slim/20261005.17" || spec.RunnerSHA256 != strings.Repeat("c", 64) || spec.TemplateID != tpl.ID {
		t.Fatalf("spec = %+v, %v", spec, err)
	}
	var layerTar bytes.Buffer
	if err := h.s.WriteLayer(ctx, tpl.BuildEnvID, &layerTar); err != nil || !bytes.Contains(layerTar.Bytes(), []byte("\x7fELF")) {
		t.Fatalf("layer: %v", err)
	}
	if _, err := h.s.BuildSpec(ctx, "job-env"); !errors.Is(err, ingest.ErrWrongKind) {
		t.Fatalf("unknown env: %v", err)
	}

	got := h.buildToVerifyFrom(t, tpl)
	v := h.envs.started[1]
	if v.Kind != store.KindVerify || v.Template == "" || v.Env[ingest.EnvMode] != ingest.ModeSelfTest || v.Env[ingest.EnvSelfTestBlocked] != "10.1.1.1:443" {
		t.Fatalf("verify spec = %+v", v)
	}
	if !h.envs.wasDestroyed(tpl.BuildEnvID) {
		t.Fatal("the builder must be destroyed once the archive is in")
	}
	if entries, _ := filepath.Glob(filepath.Join(h.dir, "templates", "*")); len(entries) != 0 {
		t.Fatalf("archive files left: %v", entries)
	}
	if err := h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(published)); err != nil {
		t.Fatal(err)
	}
	h.s.Wait()
	done := h.state(t, tpl.ID)
	if done.State != store.TemplateActive || done.ActivatedAt.IsZero() {
		t.Fatalf("final = %+v", done)
	}
	var rep FidelityReport
	if err := json.Unmarshal(done.Report, &rep); err != nil || rep.Unexpected != 0 || len(rep.Checks) != 1 {
		t.Fatalf("report = %s, %v", done.Report, err)
	}
	if ref, _ := h.s.Active(ctx); ref != h.rt.TemplateEnvironmentRef(runtime.TemplateRef{ID: done.RuntimeRef}) {
		t.Fatalf("active ref = %q", ref)
	}
	if !h.envs.wasDestroyed(got.VerifyEnvID) {
		t.Fatal("the verify environment must be destroyed")
	}
	if _, err := h.s.Build(ctx, "manual"); err != nil {
		t.Fatalf("a new build after the first: %v", err)
	}
	if _, err := h.s.Build(ctx, "manual"); !errors.Is(err, ErrBuildRunning) {
		t.Fatalf("second concurrent build: %v", err)
	}
}

func (h *tharness) buildToVerifyFrom(t *testing.T, tpl store.Template) store.Template {
	t.Helper()
	if err := h.upload(context.Background(), tpl.BuildEnvID, []byte("rootfs-"+tpl.ID)); err != nil {
		t.Fatal(err)
	}
	h.s.Wait()
	got := h.state(t, tpl.ID)
	if got.State != store.TemplateVerifying || got.VerifyEnvID == "" || got.RuntimeRef == "" {
		t.Fatalf("after the archive = %+v", got)
	}
	return got
}

func TestFailuresNeverChangeTheActiveTemplate(t *testing.T) {
	cases := map[string]func(h *tharness, tpl store.Template) string{
		"builder reports a failure": func(h *tharness, tpl store.Template) string {
			h.s.AgentEvent(context.Background(), tpl.BuildEnvID, ingest.EventBuildFailed, h.now, map[string]any{"step": "slim", "error": "exit status 1"})
			return "build:slim"
		},
		"bad checksum": func(h *tharness, tpl store.Template) string {
			r, _ := archiveOf([]byte("abc"))
			if err := h.s.ReceiveRootFS(context.Background(), tpl.BuildEnvID, r, strings.Repeat("0", 64)); !errors.Is(err, ingest.ErrBadArchive) {
				t.Fatalf("err = %v", err)
			}
			return "archive"
		},
		"oversized archive": func(h *tharness, tpl store.Template) string {
			if err := h.upload(context.Background(), tpl.BuildEnvID, make([]byte, 2<<20)); !errors.Is(err, ingest.ErrBadArchive) {
				t.Fatalf("err = %v", err)
			}
			return "archive"
		},
		"template creation fails": func(h *tharness, tpl store.Template) string {
			h.rt.CreateTemplateErr = errors.New("upload refused")
			_ = h.upload(context.Background(), tpl.BuildEnvID, []byte("x"))
			h.s.Wait()
			return "create"
		},
		"a self-test check fails": func(h *tharness, tpl store.Template) string {
			got := h.buildToVerifyFrom(t, tpl)
			rep := okReport(published)
			rep.Checks = append(rep.Checks, ingest.Check{Name: "blocked 10.1.1.6:8006", OK: false, Detail: "reachable"})
			_ = h.s.ReceiveSelfTest(context.Background(), got.VerifyEnvID, rep)
			h.s.Wait()
			return "verify"
		},
		"verification times out": func(h *tharness, tpl store.Template) string {
			h.buildToVerifyFrom(t, tpl)
			h.now = h.now.Add(21 * time.Minute)
			h.s.Tick(context.Background())
			return "timeout:verifying"
		},
		"build times out": func(h *tharness, tpl store.Template) string {
			h.now = h.now.Add(91 * time.Minute)
			h.s.Tick(context.Background())
			return "timeout:building"
		},
		"builder stops without uploading": func(h *tharness, tpl store.Template) string {
			h.s.AgentEvent(context.Background(), tpl.BuildEnvID, ingest.EventShutdown, h.now, nil)
			return "build"
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := newService(t, nil)
			ctx := context.Background()
			tpl, err := h.s.Build(ctx, "manual")
			if err != nil {
				t.Fatal(err)
			}
			stage := run(h, tpl)
			h.s.Wait()
			got := h.state(t, tpl.ID)
			if got.State != store.TemplateFailed || got.FailureStage != stage || got.FailureReason == "" {
				t.Fatalf("version = %+v, want failed at %s", got, stage)
			}
			if ref, vmid := h.s.Active(ctx); ref != "" || vmid != 949 {
				t.Fatalf("active changed to %q %d", ref, vmid)
			}
			if !h.envs.wasDestroyed(tpl.BuildEnvID) {
				t.Fatal("builder not destroyed")
			}
			if got.VerifyEnvID != "" && !h.envs.wasDestroyed(got.VerifyEnvID) {
				t.Fatal("verify environment not destroyed")
			}
			if ids := h.rt.TemplateIDs(); len(ids) != 0 {
				t.Fatalf("half-made templates left: %v", ids)
			}
			if entries, _ := filepath.Glob(filepath.Join(h.dir, "templates", "*")); len(entries) != 0 {
				t.Fatalf("archive files left: %v", entries)
			}
		})
	}
}

func TestUnexpectedDifferencesHoldActivation(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	tpl, _ := h.s.Build(ctx, "manual")
	got := h.buildToVerifyFrom(t, tpl)
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(actual))
	h.s.Wait()
	if s := h.state(t, tpl.ID); s.State != store.TemplateReady {
		t.Fatalf("state = %s, want ready (not active) with unexpected differences", s.State)
	}
	if err := h.s.Activate(ctx, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if s := h.state(t, tpl.ID); s.State != store.TemplateActive {
		t.Fatalf("manual activation: %s", s.State)
	}
	if err := h.s.Activate(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("activate missing: %v", err)
	}
}

func TestPinningHoldsAutoActivation(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	boot, _ := h.db.ActiveTemplate(ctx)
	if err := h.s.Pin(ctx, boot.ID, true); err != nil {
		t.Fatal(err)
	}
	tpl, _ := h.s.Build(ctx, "manual")
	got := h.buildToVerifyFrom(t, tpl)
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(published))
	h.s.Wait()
	if s := h.state(t, tpl.ID); s.State != store.TemplateReady {
		t.Fatalf("pinned: new version is %s, want ready", s.State)
	}
}

func TestRetentionKeepsActiveAndPreviousAndSkipsInUse(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	var built []store.Template
	var inUse runtime.Ref
	for i := 0; i < 3; i++ {
		if i == 1 {
			// A job runs on the first built template while newer ones arrive.
			inUse, _ = h.rt.Create(ctx, runtime.EnvironmentSpec{ID: "job1", Hostname: "h", Cores: 1, MemoryMB: 512,
				Template: h.rt.TemplateEnvironmentRef(runtime.TemplateRef{ID: built[0].RuntimeRef})})
		}
		tpl, err := h.s.Build(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		got := h.buildToVerifyFrom(t, tpl)
		_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(published))
		h.s.Wait()
		built = append(built, h.state(t, tpl.ID))
		h.now = h.now.Add(time.Minute)
	}
	oldest := built[0]
	h.s.Tick(ctx)
	if s := h.state(t, oldest.ID); s.State != store.TemplateRetired {
		t.Fatalf("oldest = %s, want retired while in use", s.State)
	}
	if s := h.state(t, built[1].ID); s.State != store.TemplateReady {
		t.Fatalf("previous = %s, want ready (kept for roll-back)", s.State)
	}
	_ = h.rt.Destroy(ctx, inUse)
	h.s.Tick(ctx)
	if s := h.state(t, oldest.ID); s.State != store.TemplateDeleted {
		t.Fatalf("oldest = %s, want deleted once unused", s.State)
	}
	if ids := h.rt.TemplateIDs(); len(ids) != 2 {
		t.Fatalf("runtime templates = %v", ids)
	}
	boot, _ := h.db.ListTemplates(ctx)
	for _, tp := range boot {
		if tp.Trigger == "bootstrap" && tp.State == store.TemplateDeleted {
			t.Fatal("the bootstrap template must never be deleted")
		}
	}
}

func TestRecoverFailsInterruptedBuilds(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	tpl, _ := h.s.Build(ctx, "manual")
	h.s.Recover(ctx)
	got := h.state(t, tpl.ID)
	if got.State != store.TemplateFailed || got.FailureStage != "interrupted" || !h.envs.wasDestroyed(tpl.BuildEnvID) {
		t.Fatalf("after recover = %+v", got)
	}
}

func TestCheckerBuildsOnNewReleases(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	h.s.Tick(ctx) // the bootstrap template has no versions: build once
	list, _ := h.db.ListTemplates(ctx)
	if len(list) != 2 || list[0].Trigger != "bootstrap-replacement" {
		t.Fatalf("templates = %+v", list)
	}
	got := h.buildToVerifyFrom(t, list[0])
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(published))
	h.s.Wait()
	h.now = h.now.Add(25 * time.Hour)
	h.s.Tick(ctx)
	if list, _ := h.db.ListTemplates(ctx); len(list) != 2 {
		t.Fatalf("no new release: %d versions", len(list))
	}
	h.rel.runner = RunnerRelease{Version: "2.339.0", SHA256: strings.Repeat("d", 64)}
	h.now = h.now.Add(25 * time.Hour)
	h.s.Tick(ctx)
	list, _ = h.db.ListTemplates(ctx)
	if len(list) != 3 || list[0].Trigger != "runner-release" || list[0].RunnerVersion != "2.339.0" {
		t.Fatalf("after a runner release = %+v", list[0])
	}
}
