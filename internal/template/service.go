package template

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/ids"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/template/layer"
)

var (
	// ErrBuildRunning means another build or verification is in progress.
	ErrBuildRunning = errors.New("template: a build is already running")
	// ErrDisabled means template builds are not configured (templates.vmid_range).
	ErrDisabled = errors.New("template: template builds are not configured")
	// ErrNotReady means the version cannot be activated in its current state.
	ErrNotReady = errors.New("template: only a ready version can be activated")
)

// Environments starts and destroys build and verify environments (the controller).
type Environments interface {
	StartSpecial(ctx context.Context, s controller.SpecialSpec) (string, error)
	RequestDestroy(ctx context.Context, id string) error
}

// Deps are the service's collaborators.
type Deps struct {
	Store        *store.Store
	Recorder     *events.Recorder
	Logs         *logs.Store
	Runtime      runtime.Templates
	Environments Environments
	Releases     Releases
	Config       config.Templates
	// Cache is the registry cache job templates pull through; disabled when empty.
	Cache config.Cache
	// FirewallProbe is the address the control plane serves for job agents to check the job
	// network's firewall ("" when it serves none); verification checks the group drops it.
	FirewallProbe string
	// BootstrapVMID is proxmox.template_vmid, the template used until the first build is active.
	BootstrapVMID int
	DataDir       string
	Now           func() time.Time
}

// Service builds, verifies, activates and retires templates (spec §8).
type Service struct {
	d  Deps
	mu sync.Mutex
	wg sync.WaitGroup

	lastCheck time.Time
	recheck   atomic.Bool // a profile changed or a build ended: check before the interval passes
	// lastProfile is the profile built last; the checker starts after it.
	lastProfile string
	uploading   map[string]bool // versions with a root filesystem upload in flight

	cacheMu sync.Mutex
	inUse   map[string]inUseEntry
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, uploading: map[string]bool{}, inUse: map[string]inUseEntry{}}
}

func (s *Service) now() time.Time { return s.d.Now() }

// Wait blocks until background work (template creation, verification) finishes.
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) archivePath(id string) string {
	return filepath.Join(s.d.DataDir, "templates", id+".tar.zst")
}

func (s *Service) record(ctx context.Context, level, kind, msg string, t store.Template, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["template_id"] = t.ID
	refs := events.Refs{EnvironmentID: t.BuildEnvID}
	if t.State == store.TemplateVerifying && t.VerifyEnvID != "" {
		refs.EnvironmentID = t.VerifyEnvID
	}
	_, _ = s.d.Recorder.Record(ctx, store.Event{Kind: kind, Level: level, Message: msg, EnvironmentID: refs.EnvironmentID, Data: data})
}

// EnsureBootstrap registers the configured template as the active version when the
// store has none, so job environments and builds have a template to clone.
func (s *Service) EnsureBootstrap(ctx context.Context) error {
	list, err := s.d.Store.ListTemplates(ctx)
	if err != nil || len(list) > 0 {
		return err
	}
	t := store.Template{ID: ids.NewEnvironmentID(), State: store.TemplateActive, VMID: s.d.BootstrapVMID, Trigger: "bootstrap", ActivatedAt: s.now()}
	return s.d.Store.CreateTemplate(ctx, t)
}

// firewallGated reports whether a self-test proves the gate: the agent has it, and the
// job security group drops the probe.
func firewallGated(rep ingest.SelfTestReport) bool {
	if !slices.Contains(rep.Features, ingest.FeatureFirewallGate) {
		return false
	}
	for _, c := range rep.Checks {
		if c.Name == ingest.CheckFirewallProbe {
			return c.OK && !c.Warning
		}
	}
	return false
}

// probeDetail says why a verified template does not gate.
func probeDetail(rep ingest.SelfTestReport) string {
	if !slices.Contains(rep.Features, ingest.FeatureFirewallGate) {
		return "its agent cannot wait for the firewall"
	}
	for _, c := range rep.Checks {
		if c.Name == ingest.CheckFirewallProbe && c.Detail != "" {
			return c.Detail
		}
	}
	return "the firewall probe was not checked"
}

func inProgress(state string) bool {
	return state == store.TemplateBuilding || state == store.TemplateCreating || state == store.TemplateVerifying
}

// Enabled reports whether template builds are configured.
func (s *Service) Enabled() bool { return s.d.Config.Enabled() }

// InUse reports whether an environment still depends on a built version: a live environment
// recorded as cloned from it, or a linked clone the runtime sees. Results are cached briefly
// because the runtime check costs several hypervisor calls.
func (s *Service) InUse(ctx context.Context, t store.Template) bool {
	if t.RuntimeRef == "" {
		return false
	}
	if t.VMID != 0 {
		live, err := s.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: liveEnvStates})
		if err != nil {
			return true
		}
		for _, e := range live {
			if e.TemplateVMID == t.VMID {
				return true
			}
		}
	}
	s.cacheMu.Lock()
	if c, ok := s.inUse[t.RuntimeRef]; ok && s.now().Sub(c.at) < inUseCacheFor {
		s.cacheMu.Unlock()
		return c.used
	}
	s.cacheMu.Unlock()
	used, err := s.d.Runtime.TemplateInUse(ctx, runtime.TemplateRef{ID: t.RuntimeRef})
	used = err != nil || used
	s.cacheMu.Lock()
	s.inUse[t.RuntimeRef] = inUseEntry{used: used, at: s.now()}
	s.cacheMu.Unlock()
	return used
}

var liveEnvStates = []string{"pending", "provisioning", "booting", "connected", "idle", "running", "completing", "failed", "destroying"}

const inUseCacheFor = 15 * time.Second

type inUseEntry struct {
	used bool
	at   time.Time
}

// Running reports whether a build or verification is in progress.
func (s *Service) Running(ctx context.Context) bool {
	list, _ := s.d.Store.ListTemplates(ctx)
	for _, t := range list {
		if inProgress(t.State) {
			return true
		}
	}
	return false
}

// layerVersionPrefix is the layer files' version; the agent binary's hash completes it.
func layerVersionPrefix() string { return layer.Version + "." }

// layerVersion identifies what the layer installs: the layer files, the ghrm-agent binary,
// the cache's settings and the profile, so any change triggers a rebuild (spec §8.5).
func (s *Service) layerVersion(p Profile) string {
	sum, err := s.agentSHA256()
	if err != nil {
		sum = "no-agent" // still tell profiles apart
	}
	h := sha256.Sum256([]byte(sum + "\n" + s.d.Cache.Mirrors() + "\n" + p.Hash()))
	return layerVersionPrefix() + hex.EncodeToString(h[:])[:12]
}

// Build starts a template build of a profile. Only one build runs at a time.
func (s *Service) Build(ctx context.Context, trigger, profile string) (store.Template, error) {
	if !s.d.Config.Enabled() {
		return store.Template{}, ErrDisabled
	}
	prof, err := s.Profile(ctx, profileOrDefault(profile))
	if err != nil {
		return store.Template{}, err
	}
	if s.Running(ctx) {
		return store.Template{}, ErrBuildRunning
	}
	slim, err := s.d.Releases.LatestSlim(ctx)
	if err != nil {
		return store.Template{}, fmt.Errorf("template: resolve the ubuntu-slim release: %w", err)
	}
	run, err := s.d.Releases.LatestRunner(ctx)
	if err != nil {
		return store.Template{}, fmt.Errorf("template: resolve the runner release: %w", err)
	}
	lv := s.layerVersion(prof)
	t := store.Template{ID: ids.NewEnvironmentID(), SlimRelease: slim.Version, RunnerVersion: run.Version, LayerVersion: lv,
		RunnerSHA256: run.SHA256, State: store.TemplateBuilding, Trigger: trigger, Profile: prof.Name, ProfileSpec: prof.JSON()}
	// The "building" row is the guard against concurrent builds; the lock covers only its creation,
	// not the slow start of the builder environment.
	s.mu.Lock()
	if s.Running(ctx) {
		s.mu.Unlock()
		return store.Template{}, ErrBuildRunning
	}
	err = s.d.Store.CreateTemplate(ctx, t)
	s.mu.Unlock()
	if err != nil {
		return store.Template{}, err
	}
	s.record(ctx, "info", "template.build_started", fmt.Sprintf("building template %s of profile %s (ubuntu-slim %s, runner %s, layer %s)",
		t.ID, prof.Name, slim.Version, run.Version, lv), t, map[string]any{"trigger": trigger, "profile": prof.Name})

	// Builders clone the default profile's template: it has everything a build needs.
	ref, vmid := s.Active(ctx, store.DefaultProfile)
	envID, err := s.d.Environments.StartSpecial(ctx, controller.SpecialSpec{Kind: store.KindBuild, Template: ref, TemplateVMID: vmid,
		Cores: s.d.Config.BuilderCores, MemoryMB: s.d.Config.BuilderMemoryMB, DiskGB: s.d.Config.BuilderDiskGB,
		Env:       map[string]string{ingest.EnvMode: ingest.ModeBuild},
		OnCreated: func(id string) { s.recordEnv(ctx, t.ID, id, false) }})
	s.mu.Lock()
	cur, gerr := s.d.Store.GetTemplate(ctx, t.ID)
	if err == nil && gerr == nil && cur.State != store.TemplateBuilding && envID != "" {
		// Failed (timeout, restart) while the builder was starting: do not leave it behind.
		_ = s.d.Environments.RequestDestroy(ctx, envID)
	}
	if err != nil {
		s.failLocked(ctx, t.ID, "builder", err.Error())
	}
	s.mu.Unlock()
	t, _ = s.d.Store.GetTemplate(ctx, t.ID)
	return t, nil
}

// recordEnv stores the build or verify environment of a version as soon as it exists.
func (s *Service) recordEnv(ctx context.Context, id, envID string, verify bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.d.Store.GetTemplate(ctx, id)
	if err != nil {
		return
	}
	if !inProgress(t.State) {
		_ = s.d.Environments.RequestDestroy(context.WithoutCancel(ctx), envID)
		return
	}
	if verify {
		t.VerifyEnvID = envID
	} else {
		t.BuildEnvID = envID
	}
	_ = s.d.Store.UpdateTemplate(ctx, t)
}

// templateFor finds the in-progress version an environment works for.
func (s *Service) templateFor(ctx context.Context, envID string) (store.Template, bool) {
	list, _ := s.d.Store.ListTemplates(ctx)
	for _, t := range list {
		if inProgress(t.State) && envID != "" && (t.BuildEnvID == envID || t.VerifyEnvID == envID) {
			return t, true
		}
	}
	return store.Template{}, false
}

// BuildSpec implements ingest.BuildService.
func (s *Service) BuildSpec(ctx context.Context, envID string) (ingest.BuildSpec, error) {
	t, ok := s.templateFor(ctx, envID)
	if !ok {
		return ingest.BuildSpec{}, ingest.ErrWrongKind
	}
	prof, err := s.profileOf(ctx, t)
	if err != nil {
		return ingest.BuildSpec{}, err
	}
	spec := ingest.BuildSpec{TemplateID: t.ID, SlimTag: slimPrefix + t.SlimRelease, RunnerVersion: t.RunnerVersion,
		RunnerSHA256: t.RunnerSHA256, LayerVersion: t.LayerVersion, CacheMirrors: s.d.Cache.Mirrors(), Remove: prof.Remove,
		RemoveReport: prof.removedToolNames()}
	if t.BuildEnvID == envID {
		spec.AgentSHA256, _ = s.agentSHA256()
	}
	return spec, nil
}

func (s *Service) agentSHA256() (string, error) {
	f, err := os.Open(s.agentPath())
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteAgent implements ingest.BuildService: the control plane's ghrm-agent binary.
func (s *Service) WriteAgent(ctx context.Context, envID string, w io.Writer) error {
	t, ok := s.templateFor(ctx, envID)
	if !ok || t.BuildEnvID != envID {
		return ingest.ErrWrongKind
	}
	f, err := os.Open(s.agentPath())
	if err != nil {
		return fmt.Errorf("template: ghrm-agent binary: %w", err)
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// WriteLayer implements ingest.BuildService: the layer files and the ghrm-agent binary.
func (s *Service) WriteLayer(ctx context.Context, envID string, w io.Writer) error {
	t, ok := s.templateFor(ctx, envID)
	if !ok || t.BuildEnvID != envID {
		return ingest.ErrWrongKind
	}
	f, err := os.Open(s.agentPath())
	if err != nil {
		return fmt.Errorf("template: ghrm-agent binary: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	prof, err := s.profileOf(ctx, t)
	if err != nil {
		return err
	}
	return layer.Tar(w, f, st.Size(), prof.BuildFiles())
}

func (s *Service) agentPath() string {
	if s.d.Config.AgentPath != "" {
		return s.d.Config.AgentPath
	}
	exe, err := os.Executable()
	if err != nil {
		return "ghrm-agent"
	}
	return filepath.Join(filepath.Dir(exe), "ghrm-agent")
}

// ReceiveRootFS implements ingest.BuildService: it stores the archive (streamed, hashed and
// size-capped), then creates and verifies the template in the background.
func (s *Service) ReceiveRootFS(ctx context.Context, envID string, r io.Reader, sum string) error {
	t, ok := s.templateFor(ctx, envID)
	if !ok || t.BuildEnvID != envID || t.State != store.TemplateBuilding {
		return ingest.ErrWrongKind
	}
	s.mu.Lock()
	if s.uploading[t.ID] {
		s.mu.Unlock()
		return fmt.Errorf("%w: an upload for this build is already in progress", ingest.ErrWrongKind)
	}
	s.uploading[t.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.uploading, t.ID)
		s.mu.Unlock()
	}()
	path := s.archivePath(t.ID)
	size, err := s.storeArchive(r, path, sum)
	if err != nil {
		_ = os.Remove(path)
		s.fail(context.WithoutCancel(ctx), t.ID, "archive", err.Error())
		return fmt.Errorf("%w: %v", ingest.ErrBadArchive, err)
	}
	s.mu.Lock()
	t, err = s.d.Store.GetTemplate(ctx, t.ID)
	if err != nil || t.State != store.TemplateBuilding {
		s.mu.Unlock()
		_ = os.Remove(path)
		return ingest.ErrWrongKind
	}
	t.State, t.ArchiveSHA256, t.SizeBytes = store.TemplateCreating, sum, size
	err = s.d.Store.UpdateTemplate(ctx, t)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.record(ctx, "info", "template.archive", fmt.Sprintf("template %s: received %d bytes from the builder", t.ID, size), t, map[string]any{"size": size})
	_ = s.d.Environments.RequestDestroy(context.WithoutCancel(ctx), envID)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.createAndVerify(context.WithoutCancel(ctx), t.ID)
	}()
	return nil
}

func (s *Service) storeArchive(r io.Reader, path, want string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return 0, err
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, err
	}
	h := sha256.New()
	limit := s.d.Config.MaxArchiveBytes
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	cerr := f.Close()
	switch {
	case err != nil:
	case cerr != nil:
		err = cerr
	case n > limit:
		err = fmt.Errorf("the archive is larger than templates.max_archive_bytes (%d)", limit)
	case hex.EncodeToString(h.Sum(nil)) != want:
		err = fmt.Errorf("the archive's SHA-256 is %s, the builder announced %s", hex.EncodeToString(h.Sum(nil)), want)
	}
	if err != nil {
		_ = os.Remove(part)
		return 0, err
	}
	return n, os.Rename(part, path)
}

// createAndVerify turns the archive into a template and starts its verification.
func (s *Service) createAndVerify(ctx context.Context, id string) {
	t, err := s.d.Store.GetTemplate(ctx, id)
	if err != nil {
		return
	}
	path := s.archivePath(id)
	f, err := os.Open(path)
	if err != nil {
		s.fail(ctx, id, "create", err.Error())
		return
	}
	ref, err := s.d.Runtime.CreateTemplate(ctx, runtime.TemplateSpec{ID: id, Archive: f, Size: t.SizeBytes, SHA256: t.ArchiveSHA256})
	_ = f.Close()
	_ = os.Remove(path)
	if err != nil {
		s.fail(ctx, id, "create", err.Error())
		return
	}
	s.mu.Lock()
	t, err = s.d.Store.GetTemplate(ctx, id)
	if err != nil || t.State != store.TemplateCreating {
		s.mu.Unlock()
		_ = s.d.Runtime.DeleteTemplate(ctx, ref) // failed (timeout) while creating
		return
	}
	t.RuntimeRef, t.VMID = ref.ID, vmidOf(ref.ID)
	t.State = store.TemplateVerifying
	err = s.d.Store.UpdateTemplate(ctx, t)
	s.mu.Unlock()
	if err != nil {
		s.fail(ctx, id, "create", err.Error())
		return
	}
	s.record(ctx, "info", "template.created", fmt.Sprintf("template %s created as %s; verifying", id, ref.ID), t, map[string]any{"ref": ref.ID})
	envID, err := s.d.Environments.StartSpecial(ctx, controller.SpecialSpec{Kind: store.KindVerify,
		Template: s.d.Runtime.TemplateEnvironmentRef(ref), TemplateVMID: t.VMID, Cores: 2, MemoryMB: 4096,
		Env: map[string]string{ingest.EnvMode: ingest.ModeSelfTest,
			// The cache's metrics and exporter ports must stay closed to jobs; its mirrors must answer.
			ingest.EnvSelfTestBlocked: strings.Join(append(append([]string{}, s.d.Config.SelfTestBlocked...), s.d.Cache.PrivateAddrs()...), ","),
			ingest.EnvSelfTestMirrors: strings.Join(s.d.Cache.MirrorAddrs(), ","),
			ingest.EnvFirewallProbe:   s.d.FirewallProbe},
		OnCreated: func(envID string) { s.recordEnv(ctx, id, envID, true) }})
	_ = envID
	if err != nil {
		s.fail(ctx, id, "verify", "the verify environment did not start: "+err.Error())
	}
}

func vmidOf(ref string) int {
	v, _ := strconv.Atoi(strings.SplitN(ref, "/", 2)[0])
	return v
}

// ReceiveSelfTest implements ingest.BuildService.
func (s *Service) ReceiveSelfTest(ctx context.Context, envID string, rep ingest.SelfTestReport) error {
	t, ok := s.templateFor(ctx, envID)
	if !ok || t.VerifyEnvID != envID || t.State != store.TemplateVerifying {
		return ingest.ErrWrongKind
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.conclude(context.WithoutCancel(ctx), t.ID, rep)
	}()
	return nil
}

// conclude compares the reports, then makes the version ready (and active when allowed) or failed.
func (s *Service) conclude(ctx context.Context, id string, rep ingest.SelfTestReport) {
	defer s.recheck.Store(true) // the next profile may build now
	var failed []string
	for _, c := range rep.Checks {
		if !c.OK {
			failed = append(failed, c.Name)
		}
	}
	t, err := s.d.Store.GetTemplate(ctx, id)
	if err != nil {
		return
	}
	var fid FidelityReport
	published, perr := s.d.Releases.PublishedReport(ctx, Release{Tag: slimPrefix + t.SlimRelease, Version: t.SlimRelease})
	switch {
	case perr != nil:
		fid = FidelityReport{Checks: rep.Checks, Differences: []Difference{}, Unexpected: -1, Note: "the published report could not be read: " + perr.Error()}
	case len(rep.Software) == 0:
		fid = FidelityReport{Checks: rep.Checks, Differences: []Difference{}, Unexpected: -1, Note: "the template produced no software report"}
	default:
		if fid, err = CompareReports(published, rep.Software, rep.Checks); err != nil {
			fid = FidelityReport{Checks: rep.Checks, Differences: []Difference{}, Unexpected: -1, Note: err.Error()}
		} else if prof, perr := s.profileOf(ctx, t); perr == nil {
			fid.ExplainProfile(prof)
		}
	}
	raw, _ := json.Marshal(fid)

	s.mu.Lock()
	t, err = s.d.Store.GetTemplate(ctx, id)
	if err != nil || t.State != store.TemplateVerifying {
		s.mu.Unlock()
		return
	}
	t.Report = raw
	t.FirewallGate = firewallGated(rep)
	if len(failed) > 0 {
		_ = s.d.Store.UpdateTemplate(ctx, t)
		s.failLocked(ctx, id, "verify", "self-test checks failed: "+strings.Join(failed, ", "))
		s.mu.Unlock()
		return
	}
	t.State = store.TemplateReady
	err = s.d.Store.UpdateTemplate(ctx, t)
	s.mu.Unlock()
	if err != nil {
		return
	}
	_ = s.d.Environments.RequestDestroy(ctx, t.VerifyEnvID)
	if s.d.FirewallProbe != "" && !t.FirewallGate {
		s.record(ctx, "warn", "template.firewall_delay", "job environments of this template wait the fixed firewall delay: "+probeDetail(rep), t, nil)
	}
	s.record(ctx, "info", "template.ready", fmt.Sprintf("template %s passed verification (%d unexpected differences)", id, fid.Unexpected), t,
		map[string]any{"unexpected": fid.Unexpected})

	switch {
	case !s.d.Config.AutoActivate:
		s.record(ctx, "info", "template.held", "automatic activation is off; activate the new template from the Templates page", t, nil)
	case s.pinned(ctx, t.Profile):
		s.record(ctx, "info", "template.held", "a pinned template blocks automatic activation", t, nil)
	case fid.Unexpected != 0:
		s.record(ctx, "warn", "template.held", "the software report differs from GitHub's in ways the ghrm layer does not explain; review it before activating", t,
			map[string]any{"unexpected": fid.Unexpected})
	default:
		_ = s.Activate(ctx, id)
	}
}

func (s *Service) pinned(ctx context.Context, profile string) bool {
	list, _ := s.d.Store.ListTemplates(ctx)
	for _, t := range list {
		if t.Profile == profile && t.Pinned && (t.State == store.TemplateReady || t.State == store.TemplateActive) {
			return true
		}
	}
	return false
}

// Activate makes a ready version active; the previous active version stays for roll-back.
func (s *Service) Activate(ctx context.Context, id string) error {
	s.mu.Lock()
	t, err := s.d.Store.GetTemplate(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if t.State == store.TemplateActive {
		s.mu.Unlock()
		return nil
	}
	if t.State != store.TemplateReady {
		s.mu.Unlock()
		return fmt.Errorf("%w (it is %s)", ErrNotReady, t.State)
	}
	err = s.d.Store.SetActiveTemplate(ctx, id, s.now())
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.record(ctx, "info", "template.activated", "template "+id+" is now active; new environments clone it", t, nil)
	s.retain(ctx)
	return nil
}

// Pin marks a version pinned (no automatic activation while any version is pinned).
func (s *Service) Pin(ctx context.Context, id string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.d.Store.GetTemplate(ctx, id)
	if err != nil {
		return err
	}
	if pinned && t.State != store.TemplateReady && t.State != store.TemplateActive {
		return fmt.Errorf("%w: only ready or active versions can be pinned (it is %s)", ErrNotReady, t.State)
	}
	t.Pinned = pinned
	if err := s.d.Store.UpdateTemplate(ctx, t); err != nil {
		return err
	}
	what := "unpinned"
	if pinned {
		what = "pinned"
	}
	s.record(ctx, "info", "template."+what, "template "+id+" "+what, t, nil)
	return nil
}

// AgentEvent implements controller.TemplateEvents.
func (s *Service) AgentEvent(ctx context.Context, envID, name string, _ time.Time, data map[string]any) {
	t, ok := s.templateFor(ctx, envID)
	if !ok {
		return
	}
	switch name {
	case ingest.EventBuildStep:
		step, _ := data["step"].(string)
		s.record(ctx, "info", "template.step", "template "+t.ID+": "+step, t, map[string]any{"step": step})
	case ingest.EventBuildFailed:
		step, _ := data["step"].(string)
		msg, _ := data["error"].(string)
		s.fail(ctx, t.ID, "build:"+step, msg)
	case ingest.EventShutdown:
		switch {
		case t.State == store.TemplateBuilding && t.BuildEnvID == envID:
			s.fail(ctx, t.ID, "build", "the builder stopped without uploading a root filesystem")
		case t.State == store.TemplateVerifying && t.VerifyEnvID == envID && len(t.Report) == 0:
			// The report request may still be in flight; Tick fails the version if it never comes.
		}
	}
}

// fail marks a version failed and removes everything the build made.
func (s *Service) fail(ctx context.Context, id, stage, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLocked(ctx, id, stage, reason)
}

func (s *Service) failLocked(ctx context.Context, id, stage, reason string) {
	t, err := s.d.Store.GetTemplate(ctx, id)
	if err != nil || !inProgress(t.State) {
		return
	}
	s.recheck.Store(true) // the next profile may build now
	t.State, t.FailureStage, t.FailureReason = store.TemplateFailed, stage, reason
	if err := s.d.Store.UpdateTemplate(ctx, t); err != nil {
		return
	}
	for _, env := range []string{t.BuildEnvID, t.VerifyEnvID} {
		if env != "" {
			_ = s.d.Environments.RequestDestroy(ctx, env)
		}
	}
	if t.RuntimeRef != "" {
		_ = s.d.Runtime.DeleteTemplate(ctx, runtime.TemplateRef{ID: t.RuntimeRef})
	} else {
		// The runtime may have made the template without the ref being recorded (a restart).
		_ = s.d.Runtime.CleanupTemplate(ctx, id)
	}
	_ = os.Remove(s.archivePath(id))
	_ = os.Remove(s.archivePath(id) + ".part")
	s.record(ctx, "error", "template.failed", fmt.Sprintf("template %s failed at %s: %s", id, stage, reason), t, map[string]any{"stage": stage})
}

// Recover fails versions a previous run left mid-build and cleans up after them.
func (s *Service) Recover(ctx context.Context) {
	list, err := s.d.Store.ListTemplates(ctx)
	if err != nil {
		return
	}
	for _, t := range list {
		if inProgress(t.State) {
			s.fail(ctx, t.ID, "interrupted", "the control plane restarted during the "+t.State+" stage")
		}
	}
}

// Run enforces timeouts, retention and the release check until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs one round of timeouts, retention and release checks.
func (s *Service) Tick(ctx context.Context) {
	now := s.now()
	list, err := s.d.Store.ListTemplates(ctx)
	if err != nil {
		return
	}
	for _, t := range list {
		var limit time.Duration
		switch t.State {
		case store.TemplateBuilding, store.TemplateCreating:
			limit = s.d.Config.BuildTimeout.Std()
			if t.State == store.TemplateBuilding && s.envGone(ctx, t.BuildEnvID) {
				s.fail(ctx, t.ID, "build", "the builder environment is gone")
				continue
			}
		case store.TemplateVerifying:
			limit = s.d.Config.VerifyTimeout.Std()
			if t.VerifyEnvID != "" && s.envGone(ctx, t.VerifyEnvID) {
				s.fail(ctx, t.ID, "verify", "the verify environment is gone before reporting")
				continue
			}
		default:
			continue
		}
		since := t.UpdatedAt
		if t.State == store.TemplateBuilding {
			since = t.CreatedAt
		}
		if limit > 0 && now.Sub(since) > limit {
			s.fail(ctx, t.ID, "timeout:"+t.State, fmt.Sprintf("took longer than %s", limit))
		}
	}
	s.retain(ctx)
	s.check(ctx, now)
}

// envGone reports whether a build or verify environment stopped working: destroyed, failed,
// missing, or powered off (completing) for more than a minute without its result arriving.
func (s *Service) envGone(ctx context.Context, envID string) bool {
	if envID == "" {
		return false
	}
	e, err := s.d.Store.GetEnvironment(ctx, envID)
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	switch e.State {
	case "destroyed", "failed", "destroying":
		return true
	case "completing":
		return s.now().Sub(e.StateChangedAt) > time.Minute
	}
	return false
}

// retain keeps the active version, the newest Keep-1 previously active versions (roll-back
// targets), the newest never-activated candidate awaiting review, and every pinned version.
// Other built versions are retired and deleted once no environment depends on them. The
// bootstrap template is never touched.
func (s *Service) retain(ctx context.Context) {
	list, err := s.d.Store.ListTemplates(ctx)
	if err != nil {
		return
	}
	s.retainFrom(ctx, list)
}

func (s *Service) retainFrom(ctx context.Context, list []store.Template) {
	byProfile := map[string][]store.Template{}
	for _, t := range list {
		byProfile[t.Profile] = append(byProfile[t.Profile], t)
	}
	for _, versions := range byProfile {
		s.retireBeyond(ctx, versions)
	}
	s.deleteRetired(ctx)
}

// retireBeyond retires one profile's ready versions beyond those kept (list is newest first).
func (s *Service) retireBeyond(ctx context.Context, list []store.Template) {
	var previous, candidates []store.Template
	for _, t := range list {
		if t.RuntimeRef == "" || t.State != store.TemplateReady || t.Pinned {
			continue
		}
		if !t.ActivatedAt.IsZero() {
			previous = append(previous, t)
		} else {
			candidates = append(candidates, t)
		}
	}
	sort.SliceStable(previous, func(i, j int) bool { return previous[i].ActivatedAt.After(previous[j].ActivatedAt) })
	var retire []store.Template
	if keep := s.d.Config.Keep - 1; len(previous) > keep {
		retire = append(retire, previous[max(keep, 0):]...)
	}
	if len(candidates) > 1 {
		retire = append(retire, candidates[1:]...) // list is newest first
	}
	for _, t := range retire {
		s.mu.Lock()
		cur, err := s.d.Store.GetTemplate(ctx, t.ID)
		if err != nil || cur.State != store.TemplateReady || cur.Pinned {
			s.mu.Unlock() // activated or pinned meanwhile
			continue
		}
		cur.State = store.TemplateRetired
		err = s.d.Store.UpdateTemplate(ctx, cur)
		s.mu.Unlock()
		if err == nil {
			s.record(ctx, "info", "template.retired", "template "+cur.ID+" retired (beyond the versions kept for roll-back)", cur, nil)
		}
	}
}

// deleteRetired deletes retired versions no environment depends on.
func (s *Service) deleteRetired(ctx context.Context) {
	all, err := s.d.Store.ListTemplates(ctx)
	if err != nil {
		return
	}
	for _, t := range all {
		if t.State != store.TemplateRetired || t.RuntimeRef == "" {
			continue
		}
		s.cacheMu.Lock()
		delete(s.inUse, t.RuntimeRef) // decide on fresh data
		s.cacheMu.Unlock()
		if s.InUse(ctx, t) {
			continue
		}
		if err := s.d.Runtime.DeleteTemplate(ctx, runtime.TemplateRef{ID: t.RuntimeRef}); err != nil {
			continue
		}
		s.mu.Lock()
		cur, err := s.d.Store.GetTemplate(ctx, t.ID)
		if err == nil && cur.State == store.TemplateRetired {
			cur.State = store.TemplateDeleted
			_ = s.d.Store.UpdateTemplate(ctx, cur)
		}
		s.mu.Unlock()
		s.record(ctx, "info", "template.deleted", "template "+t.ID+" deleted", t, nil)
	}
}

// check starts a build when a profile's active version is behind: a new slim release,
// runner release or layer version (the layer includes the profile), or none built yet.
// One build runs at a time; when it ends the next profile is checked at once, starting
// after the profile built last, so one failing profile cannot hold the others back.
func (s *Service) check(ctx context.Context, now time.Time) {
	iv := s.d.Config.CheckInterval.Std()
	due := s.lastCheck.IsZero() || now.Sub(s.lastCheck) >= iv || s.recheck.Load()
	if !s.d.Config.Enabled() || iv <= 0 || !due || s.Running(ctx) {
		return
	}
	slim, err := s.d.Releases.LatestSlim(ctx)
	if err != nil {
		return
	}
	run, err := s.d.Releases.LatestRunner(ctx)
	if err != nil {
		return
	}
	profiles, err := s.Profiles(ctx)
	if err != nil {
		return
	}
	s.lastCheck = now
	s.recheck.Store(false)
	list, _ := s.d.Store.ListTemplates(ctx)
	start := 0
	for i, p := range profiles {
		if p.Name == s.lastProfile {
			start = i + 1
		}
	}
	for k := range profiles {
		p := profiles[(start+k)%len(profiles)]
		lv := s.layerVersion(p)
		active, err := s.d.Store.ActiveTemplate(ctx, p.Name)
		trigger := ""
		switch {
		case errors.Is(err, store.ErrNotFound) && p.Name != store.DefaultProfile:
			trigger = "new-profile"
		case err != nil:
			continue
		case active.RuntimeRef == "":
			trigger = "bootstrap-replacement"
		case active.SlimRelease != slim.Version:
			trigger = "slim-release"
		case active.RunnerVersion != run.Version:
			trigger = "runner-release"
		case active.LayerVersion != lv:
			trigger = "layer"
		default:
			continue
		}
		if triedAlready(list, p, slim.Version, run.Version, lv, now) {
			continue
		}
		tpl, err := s.Build(ctx, trigger, p.Name)
		if err == nil {
			s.lastProfile = p.Name
			s.record(ctx, "info", "template.check", "new inputs found ("+trigger+"); building template "+tpl.ID+" of profile "+p.Name, tpl,
				map[string]any{"trigger": trigger, "profile": p.Name})
		}
		return
	}
}

// triedAlready reports whether a version of the profile with these inputs exists, so the
// same build is not tried over and over: a failed attempt waits a day, a held one forever.
// A version retired before the profile was last saved does not count (a profile deleted
// and made again).
func triedAlready(list []store.Template, p Profile, slim, runner, lv string, now time.Time) bool {
	for _, t := range list {
		if t.Profile != p.Name || t.SlimRelease != slim || t.RunnerVersion != runner || t.LayerVersion != lv || t.State == store.TemplateDeleted {
			continue
		}
		if t.State == store.TemplateFailed && now.Sub(t.UpdatedAt) >= 24*time.Hour {
			continue
		}
		if t.State == store.TemplateRetired && !p.SavedAt.IsZero() && p.SavedAt.After(t.UpdatedAt) {
			continue
		}
		return true
	}
	return false
}
