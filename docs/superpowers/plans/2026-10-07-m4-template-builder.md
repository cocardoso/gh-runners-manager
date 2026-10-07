# M4 — ubuntu-slim Template Builder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Every step that changes behaviour starts with a failing test (superpowers:test-driven-development).

**Goal:** ghrm builds its job templates itself from GitHub's `ubuntu-slim` recipe plus a small ghrm layer, verifies each one (self-test and fidelity report), activates it, keeps the previous one for rollback, and rebuilds when a new slim release, runner release or layer version appears. The Templates page shows versions, live build logs and the fidelity report, with rebuild, pin and roll-back actions.

**Architecture:**
- A new package `internal/template` owns template versions: build orchestration, verification, activation, retention and the release checker. It persists versions in the store and records `template.*` events.
- Builds and verifications run in ordinary ghrm environments of a new **kind** (`build`, `verify`). They are cloned from the current active template, which already has Docker, systemd and `ghrm-agent`. The agent runs in **build mode** or **self-test mode**, selected by a bootstrap variable, and talks to the control plane over the existing ingest channel. New ingest endpoints carry the build spec, the layer files, the root filesystem upload and the self-test report.
- The control plane uploads the root filesystem to Proxmox template storage, creates the template LXC from it with the job settings, converts it to a template, and clones it for verification. All of this goes through new `proxmox-lxc` runtime operations behind a `runtime.Templates` interface (with an in-memory fake for tests and `ghrm demo`).
- Job environments clone from the **active** template. The controller asks a `TemplateSource` for the VMID instead of reading `proxmox.template_vmid`, which becomes the bootstrap template used until the first build is active.

**Tech Stack:** Go (existing packages: store, runtime, proxmox, ingest, agent, controller, api, demo), Docker inside the builder LXC, `zstd`, the Proxmox VE API (`storage/{storage}/upload`, `lxc` create, `lxc/{vmid}/template`, `lxc/{vmid}/firewall`), the GitHub REST API (releases), React and Kumo for the UI.

**Spec:** `docs/superpowers/specs/2026-10-07-gh-runners-manager-design.md` §8 (templates), §4 (components), §10 (networking), §11.2 item 5 (Templates page).

## Global Constraints

- All earlier Global Constraints apply: English only in the repository, no Claude attribution in commits or PRs, `CGO_ENABLED=0`, `docs/architecture.md` updated in the same change as any architecture change.
- The official `ubuntu-slim` Dockerfile is built **unmodified** at a pinned release tag `ubuntu-slim/<version>` (spec §8.1).
- The ghrm layer adds only: systemd as init, Docker Engine with the buildx and compose plugins, the `actions/runner` release with its SHA-256 verified, owned by an unprivileged `runner` user with passwordless sudo, `ghrm-agent` and its systemd unit, IPv4-only apt configuration, `LANG=C.UTF-8`, and removal of `machine-id` and SSH host keys (spec §8.2 and the spike findings).
- Template LXC settings: `unprivileged=1`, `features: nesting=1,keyctl=1`, `ostype=ubuntu`, explicit `nameserver`, NIC on the job VNet with `firewall=1`, the `gh-runner` security group attached, tag `ghrm-template` (spec §8.3 step 5).
- A failed build or verification **never** changes the active template (spec §8.5).
- Keep N=2 templates (active plus previous). Delete older ones only when no environment references them (spec §8.5).
- Mutating API actions (rebuild, pin, unpin, activate) require the admin token, like destroy.
- The demo (`ghrm demo`) and the fake runtime must exercise the whole template flow, so the UI and the browser tests need no Proxmox.

## Review Focus

1. **A build dies halfway** (builder powered off, upload interrupted, Proxmox upload or create fails, control plane restarts mid-build). The version must end `failed` with a reason, every temporary guest and file must be cleaned up (builder LXC, verify LXC, half-created template LXC, uploaded archive), and the active template must not change. Restart recovery must not leave a version stuck in `building` forever.
2. **The fidelity report differs in ways the layer does not explain** (a tool missing, a different version). The template must stay `ready` but not auto-activate when differences are unexpected, and the UI must show each difference; expected layer additions must not count.
3. **Concurrent triggers** (a daily check and a manual rebuild at once, or a rebuild while a build runs). Only one build runs at a time; a second request is rejected with a clear message.
4. **Retention while environments still use a template.** A linked clone depends on its template; deleting a template that a live environment was cloned from must never happen.
5. **Huge uploads and a full disk.** The root filesystem upload is gigabytes. It must stream (no full buffering in memory), enforce a size cap, verify its SHA-256, and report a disk-full error as a failed build.

---

### Task 1: Store — template versions and environment kinds

**Files:**
- Modify: `internal/store/migrations.go` (or the migration list in `internal/store/store.go`), `internal/store/environments.go`
- Create: `internal/store/templates.go`, `internal/store/templates_test.go`

**Interfaces — Produces:**
```go
// Template states.
const (
    TemplateBuilding  = "building"   // builder running
    TemplateCreating  = "creating"   // archive received, template LXC being created
    TemplateVerifying = "verifying"  // verify environment running
    TemplateReady     = "ready"      // verified, not active
    TemplateActive    = "active"
    TemplateFailed    = "failed"
    TemplateRetired   = "retired"    // previous versions beyond N, pending deletion
    TemplateDeleted   = "deleted"
)

type Template struct {
    ID            string    // ULID
    SlimRelease   string    // e.g. "20261005.17"
    RunnerVersion string    // e.g. "2.338.0"
    LayerVersion  string    // e.g. "1"
    State         string
    VMID          int       // template LXC, 0 until created
    Volume        string    // Proxmox volid of the uploaded archive
    ArchiveSHA256 string
    SizeBytes     int64
    Pinned        bool
    Trigger       string    // "manual", "slim-release", "runner-release", "layer", "bootstrap"
    BuildEnvID    string
    VerifyEnvID   string
    FailureStage  string
    FailureReason string
    Report        []byte    // JSON fidelity report
    CreatedAt, UpdatedAt, ActivatedAt time.Time
}

func (s *Store) CreateTemplate(ctx, t Template) error
func (s *Store) GetTemplate(ctx, id string) (Template, error)          // ErrNotFound
func (s *Store) ListTemplates(ctx) ([]Template, error)                 // newest first
func (s *Store) UpdateTemplate(ctx, t Template) error
func (s *Store) ActiveTemplate(ctx) (Template, error)                  // ErrNotFound when none
// SetActiveTemplate makes id active and the previous active "ready", in one transaction.
func (s *Store) SetActiveTemplate(ctx, id string, at time.Time) error
```
- `store.Environment` gains `Kind string` (`"job"` default, `"build"`, `"verify"`) and `TemplateVMID int` (the template it was cloned from, for retention). `EnvironmentFilter` gains `Kinds []string`; an empty filter means all kinds.

- [ ] Write failing tests: create/get/list/update round-trip including `Report` and `Pinned`; `SetActiveTemplate` demotes the previous active to `ready` and sets `ActivatedAt`; at most one active (a unique partial index `WHERE state='active'`); environments round-trip `Kind` and `TemplateVMID`, existing rows migrate to `kind='job'`; `ListEnvironments` filters by kinds.
- [ ] Run: `go test ./internal/store/` — Expected: FAIL (undefined symbols).
- [ ] Implement the migration (new `templates` table; `ALTER TABLE environments ADD COLUMN kind TEXT NOT NULL DEFAULT 'job'`, `template_vmid INTEGER NOT NULL DEFAULT 0`) and the methods.
- [ ] Run: `go test -race ./internal/store/` — Expected: PASS.
- [ ] Commit `feat(store): template versions and environment kinds`.

### Task 2: Proxmox client — template storage and LXC creation

**Files:**
- Create: `internal/proxmox/template.go`, `internal/proxmox/template_test.go`
- Modify: `internal/proxmox/proxmoxtest/` (fake server routes)

**Interfaces — Produces:**
```go
// UploadTemplate streams an archive to <storage> as content "vztmpl" and waits for the task.
// It returns the volid, e.g. "local:vztmpl/ghrm-01J…tar.zst".
func (c *Client) UploadTemplate(ctx, node, storage, filename string, r io.Reader, size int64) (string, error)
func (c *Client) DeleteVolume(ctx, node, storage, volid string) error
func (c *Client) StorageContent(ctx, node, storage, content string) ([]Volume, error) // Volume{VolID string; Size int64}

type CreateLXCOptions struct {
    VMID         int
    OSTemplate   string   // volid
    Hostname     string
    Pool         string
    Storage      string   // rootfs storage, e.g. local-lvm
    RootFSGB     int
    Cores        int
    MemoryMB     int
    Nameserver   string
    Bridge       string   // job VNet, e.g. "jobnet"
    Tags         []string
}
func (c *Client) CreateLXC(ctx, node string, o CreateLXCOptions) error // unprivileged=1, nesting+keyctl, ostype=ubuntu, net0 firewall=1, waits for the task
func (c *Client) ConvertToTemplate(ctx, node string, vmid int) error
func (c *Client) EnableFirewallGroup(ctx, node string, vmid int, group string) error // options enable=1 + rule {type: group, action: group}
func (c *Client) ResizeLXCDisk(ctx, node string, vmid int, disk string, sizeGB int) error
```
- The upload is a streaming `multipart/form-data` body built with `io.Pipe` (fields `content=vztmpl`, file `filename`), never buffered in memory; `Content-Length` is computed from the part sizes so Proxmox gets a known length.

- [ ] Write failing tests against `proxmoxtest`: the upload streams the exact bytes (fake server hashes them), a 4xx/5xx and a failed task are errors with the task log tail; `CreateLXC` sends `unprivileged=1`, `features=nesting=1,keyctl=1`, `ostype=ubuntu`, `nameserver`, `net0=name=eth0,bridge=jobnet,ip=dhcp,firewall=1`, `rootfs=local-lvm:<n>`, `pool`, `tags`; `ConvertToTemplate`, `EnableFirewallGroup`, `ResizeLXCDisk` and `DeleteVolume` hit the right paths with the right parameters.
- [ ] Run `go test ./internal/proxmox/...` — Expected: FAIL.
- [ ] Implement, extending the fake server (it records created guests so the runtime tests in Task 3 can use it).
- [ ] Run `go test -race ./internal/proxmox/...` — Expected: PASS. Commit `feat(proxmox): upload templates, create and convert LXCs, firewall groups`.

### Task 3: Runtime — per-environment templates and the template operations

**Files:**
- Modify: `internal/runtime/runtime.go`, `internal/runtime/proxmoxlxc/runtime.go`, `internal/runtime/runtimetest/fake.go`
- Create: `internal/runtime/proxmoxlxc/templates.go`, `internal/runtime/proxmoxlxc/templates_test.go`

**Interfaces — Produces:**
```go
// EnvironmentSpec gains:
    Template string // runtime template reference to clone from ("" = the configured bootstrap template)
    DiskGB   int    // 0 = keep the template's size; larger values grow the root disk after cloning

type TemplateSpec struct {
    ID       string    // template version ID (lowercase ULID)
    Archive  io.Reader // root filesystem .tar.zst
    Size     int64
}

type TemplateRef struct{ ID string } // proxmox-lxc: "<vmid>/<template id>"

// Templates is implemented by runtimes that can build templates.
type Templates interface {
    CreateTemplate(ctx context.Context, spec TemplateSpec) (TemplateRef, error)
    DeleteTemplate(ctx context.Context, ref TemplateRef) error
    TemplateInUse(ctx context.Context, ref TemplateRef) (bool, error) // a linked clone still exists
}
```
- `proxmox-lxc`:
  - `CreateTemplate`: lock, allocate a VMID from `templates.vmid_range` (Task 7 config), upload the archive as `ghrm-<id>.tar.zst`, `CreateLXC` with the job settings, `EnableFirewallGroup("gh-runner")`, `ConvertToTemplate`, tag `ghrm-template`, `ghrmtpl-<id>`. On any error after the upload: delete the half-created guest and the volume; return a wrapped error naming the stage.
  - `DeleteTemplate`: refuse unless the guest carries `ghrmtpl-<id>` (never touches foreign guests); delete the guest, then the volume.
  - `TemplateInUse`: any LXC in the pool whose config `parent`/linked-clone base is this template's disk (use the `rootfs` volume name prefix `base-<vmid>-disk-`).
  - `Create` clones from `spec.Template` when set, else `cfg.TemplateVMID`; `DiskGB` triggers `ResizeLXCDisk("rootfs", DiskGB)` before start.
- `runtimetest.Fake` implements `Templates` (stores templates in memory, records in-use from clones, can be told to fail a stage).

- [ ] Failing tests: create succeeds end to end on the fake Proxmox (asserts the call order upload → create → firewall → template → tags); a failure at each stage cleans up the guest and the volume; `DeleteTemplate` refuses a guest without the ownership tag and refuses an in-use template (`ErrTemplateInUse`); `Create` with `Template:"958"` clones 958; `DiskGB` resizes.
- [ ] Run `go test ./internal/runtime/...` — Expected: FAIL. Implement. Run with `-race` — PASS. Commit `feat(runtime): build and delete templates, clone from a chosen template`.

### Task 4: ghrm layer and release resolution

**Files:**
- Create: `template/layer/Dockerfile`, `template/layer/ghrm-agent.service`, `template/layer/apt-ipv4.conf`, `template/layer/embed.go` (package `layer`, `//go:embed`), `template/layer/layer_test.go`
- Create: `internal/template/releases.go`, `internal/template/releases_test.go`

**Interfaces — Produces:**
```go
package layer
const Version = "1"                       // bump on every change to the layer files
func Files() fs.FS                        // Dockerfile, unit, apt config
func Tar(w io.Writer, agent io.Reader, agentSize int64) error // layer files + ghrm-agent, as a build context

package template
type Release struct{ Tag, Version string }                     // ubuntu-slim/20261005.17 → 20261005.17
type RunnerRelease struct{ Version, URL, SHA256 string }      // linux-x64 tarball
type Releases interface {
    LatestSlim(ctx context.Context) (Release, error)
    LatestRunner(ctx context.Context) (RunnerRelease, error)
    PublishedReport(ctx context.Context, slim Release) ([]byte, error) // images/ubuntu-slim/ubuntu-slim-Report.json at the tag
}
func NewGitHubReleases(baseURL, rawBaseURL string, hc *http.Client) Releases
```
- The Dockerfile: `ARG BASE` (the built slim image), `ARG RUNNER_VERSION`, `ARG RUNNER_SHA256`; installs `systemd systemd-sysv dbus`, Docker CE with `docker-buildx-plugin docker-compose-plugin` from Docker's apt repo, creates `runner` with passwordless sudo and the `docker` group, downloads and checks the runner tarball (`sha256sum -c`), installs the runner dependencies (`bin/installdependencies.sh`), copies `ghrm-agent` and its unit (enabled), writes `/etc/default/locale` with `LANG=C.UTF-8`, the IPv4-only apt config, masks units that do not work in unprivileged LXC (`systemd-networkd-wait-online`, `getty@`), truncates `machine-id`, removes SSH host keys, and sets `STOPSIGNAL SIGRTMIN+3`.
- `LatestRunner` reads `GET /repos/actions/runner/releases/latest` and takes the SHA-256 from the release body marker `<!-- BEGIN SHA linux-x64 -->…<!-- END SHA linux-x64 -->`; a missing marker is an error (never build an unverified runner).
- `LatestSlim` picks the newest `ubuntu-slim/*` tag from `GET /repos/actions/runner-images/releases` (not drafts or pre-releases).

- [ ] Failing tests: `Tar` contains the four files with the agent at `ghrm-agent` mode 0755; the Dockerfile verifies the SHA (contains `sha256sum -c`) and never `curl | sh`; the releases client parses the fixtures (fake HTTP server) and errors on a missing SHA marker and on no slim release.
- [ ] Run, implement, run (PASS). Commit `feat(template): ghrm layer and GitHub release resolution`.

### Task 5: Ingest — build and self-test endpoints

**Files:**
- Modify: `internal/ingest/protocol.go`, `internal/ingest/server.go`
- Create: `internal/ingest/build.go`, `internal/ingest/build_test.go`

**Interfaces — Produces:**
```go
const (
    BuildSpecPath  = "/ingest/v1/build/spec"    // GET → BuildSpec JSON
    BuildLayerPath = "/ingest/v1/build/layer"   // GET → layer build context (tar)
    BuildRootFSPath = "/ingest/v1/build/rootfs" // PUT, body = .tar.zst, header X-Ghrm-SHA256
    SelfTestPath   = "/ingest/v1/selftest"      // POST, body = SelfTestReport JSON
    EnvMode        = "GHRM_MODE"                // "build" | "selftest" (absent = job)
)
type BuildSpec struct {
    TemplateID    string `json:"template_id"`
    SlimTag       string `json:"slim_tag"`        // ubuntu-slim/20261005.17
    RunnerVersion string `json:"runner_version"`
    RunnerSHA256  string `json:"runner_sha256"`
    LayerVersion  string `json:"layer_version"`
}
type SelfTestReport struct {
    Checks   []Check         `json:"checks"`    // Check{Name, OK bool, Detail string, Seconds float64}
    Software json.RawMessage `json:"software"`  // output of generate-software-report.sh
}
// BuildService is implemented by internal/template.
type BuildService interface {
    BuildSpec(ctx context.Context, envID string) (BuildSpec, error)
    WriteLayer(ctx context.Context, envID string, w io.Writer) error
    ReceiveRootFS(ctx context.Context, envID string, r io.Reader, sha256 string) error
    ReceiveSelfTest(ctx context.Context, envID string, rep SelfTestReport) error
}
func NewServer(resolver TokenResolver, sink EventSink, logStore *logs.Store, recorder *events.Recorder, builds BuildService) http.Handler
```
- Authentication is the same bearer token as frames. `BuildService` methods return `ErrWrongKind` for a token of a job environment (→ 403).
- The rootfs body is not limited by `MaxBodyBytes`; the template service enforces its own cap (`templates.max_archive_bytes`).
- New agent streams: `build` and `selftest` (add to `AgentStreams`, and to `logs.Streams`).

- [ ] Failing tests with a fake `BuildService`: each endpoint requires a valid token (401), a job token gets 403, the spec/layer/rootfs/selftest pass through; the rootfs is streamed (the fake reads it incrementally; a 64 MiB body works under a small memory budget); a bad SHA header is rejected by the service and surfaces as 422.
- [ ] Run, implement, run (PASS, `-race`). Commit `feat(ingest): build spec, layer, root filesystem upload and self-test endpoints`.

### Task 6: Agent — build mode and self-test mode

**Files:**
- Create: `internal/agent/build.go`, `internal/agent/selftest.go`, `internal/agent/build_test.go`, `internal/agent/selftest_test.go`, `internal/agent/exec.go` (a `Commander` interface over `os/exec` with a fake for tests)
- Modify: `cmd/ghrm-agent/main.go`, `internal/agent/bootstrap.go` (`Mode` field from `GHRM_MODE`; a build/self-test bootstrap does not need a JIT config), `internal/agent/client.go` (GET/PUT helpers sharing the TLS pinning)

**Interfaces — Produces:**
```go
type Commander interface {
    Run(ctx context.Context, name string, args []string, dir string, out func(line string)) error
}
func RunBuild(ctx context.Context, c *Client, cmd Commander, work string) error
func RunSelfTest(ctx context.Context, c *Client, cmd Commander, opts SelfTestOptions) (ingest.SelfTestReport, error)
type SelfTestOptions struct {
    Work         string
    BlockedAddrs []string // from GHRM_SELFTEST_BLOCKED (comma list): must be unreachable (LAN, hypervisor)
    ProbeURL     string   // HTTPS URL that must be reachable, default https://api.github.com
}
```
- Build steps, each streamed to the `build` log and announced as event `build_step` with `{name}`:
  1. `GET spec`; `git clone --depth 1 --branch <slim_tag> https://github.com/actions/runner-images`;
  2. `docker build -t ghrm-slim images/ubuntu-slim` (the official Dockerfile, unmodified);
  3. `GET layer` → unpack; `docker build --build-arg BASE=ghrm-slim --build-arg RUNNER_VERSION… -t ghrm-tpl .`;
  4. `docker create` + `docker export | zstd -T0 -10` to a file, computing SHA-256 while writing; `PUT rootfs` with the header;
  5. event `build_finished` (or `build_failed` with the step and the error), then power off.
- Self-test checks (each a `Check`): `docker run --rm hello-world`; a `docker buildx create --driver docker-container` build of a two-line Dockerfile; `docker compose up` of a one-service stack with a bind mount that writes a file back; DNS lookup of `github.com`; HTTPS GET of `ProbeURL`; each `BlockedAddrs` entry unreachable within 3 s; `run.sh --version` of the runner; the agent reached the ingest (implicit). Then `generate-software-report.sh` from the slim recipe (cloned at the tag) writes JSON, posted with the checks. Event `selftest_finished`, power off.

- [ ] Failing tests with the fake `Commander` and an `httptest` TLS ingest: the build runs the steps in order with the exact arguments (asserts the official Dockerfile path and no modification), uploads the archive with the right SHA, and reports `build_failed` with the step when a command fails; self-test reports each check (a failing `hello-world` yields `OK:false` with the output), blocked addresses that answer fail the check, and the report includes the software JSON.
- [ ] Run, implement, run (PASS). Commit `feat(agent): build and self-test modes`.

### Task 7: Template service — build, verify, activate, retain, check for releases

**Files:**
- Create: `internal/template/service.go`, `internal/template/fidelity.go`, `internal/template/checker.go`, tests for each
- Modify: `internal/config/config.go` (+tests), `internal/controller/controller.go`, `internal/controller/provision.go`, `internal/controller/agent.go`, `internal/controller/reaper.go`, `cmd/ghrm/serve.go`, `deploy/examples/ghrm.example.yaml`

**Interfaces — Produces:**
```go
// config
type Templates struct {
    VMIDRange       VMIDRange `yaml:"vmid_range"`        // e.g. 950–959, inside the token's pool, outside proxmox.vmid_range
    Storage         string    `yaml:"storage"`           // vztmpl storage, default "local"
    RootFSGB        int       `yaml:"rootfs_gb"`         // template disk, default 16
    BuilderDiskGB   int       `yaml:"builder_disk_gb"`   // default 40
    BuilderCores    int       `yaml:"builder_cores"`     // default 4
    BuilderMemoryMB int       `yaml:"builder_memory_mb"` // default 8192
    Keep            int       `yaml:"keep"`              // default 2
    CheckInterval   Duration  `yaml:"check_interval"`    // default 24h; 0 disables
    AutoActivate    bool      `yaml:"auto_activate"`     // default true
    BuildTimeout    Duration  `yaml:"build_timeout"`     // default 90m
    VerifyTimeout   Duration  `yaml:"verify_timeout"`    // default 20m
    MaxArchiveBytes int64     `yaml:"max_archive_bytes"` // default 8 GiB
    AgentPath       string    `yaml:"agent_path"`        // ghrm-agent binary served to builders; default: next to the ghrm executable
    SelfTestBlocked []string  `yaml:"selftest_blocked"`  // addresses that must be unreachable, e.g. the gateway's LAN IP and the Proxmox host
}

// internal/template
type Service struct{ /* store, recorder, logs, runtime.Runtime, runtime.Templates, Releases, config */ }
func NewService(d Deps) *Service
func (s *Service) Active(ctx context.Context) (vmid string, ok bool)            // implements controller.TemplateSource
func (s *Service) Build(ctx context.Context, trigger string) (store.Template, error) // ErrBuildRunning when one is in progress
func (s *Service) Activate(ctx context.Context, id string) error                 // manual activation or roll back
func (s *Service) Pin(ctx context.Context, id string, pinned bool) error
func (s *Service) AgentEvent(ctx context.Context, envID, name string, at time.Time, data map[string]any) // build/verify events
func (s *Service) Recover(ctx context.Context)                                  // at startup: fail versions stuck mid-build, destroy their guests
func (s *Service) Run(ctx context.Context)                                      // checker loop and timeouts
// ingest.BuildService is implemented by *Service.

// fidelity.go
type Difference struct {
    Kind     string `json:"kind"`     // "missing", "extra", "version"
    Name     string `json:"name"`
    Expected string `json:"expected,omitempty"`
    Actual   string `json:"actual,omitempty"`
    Explained bool  `json:"explained"` // an item the ghrm layer adds on purpose
}
type FidelityReport struct {
    Checks      []ingest.Check `json:"checks"`
    Differences []Difference   `json:"differences"`
    Unexpected  int            `json:"unexpected"`
}
func CompareReports(published, actual []byte, layerItems []string) (FidelityReport, error)
```
- Flow of `Build`:
  1. Refuse when a version is in `building`, `creating` or `verifying` (`ErrBuildRunning`).
  2. Resolve releases. Create the version (`building`). Record `template.build_started`.
  3. Provision a `build` environment through the controller's runtime: clone the active template, `DiskGB=BuilderDiskGB`, builder cores/memory, bootstrap env `GHRM_MODE=build` plus the ingest variables. The environment and its token live in the environments table (`Kind="build"`), so logs and the UI reuse the environment machinery.
  4. `ReceiveRootFS` streams the archive into `<data_dir>/templates/<id>.tar.zst.part`, hashing and counting (cap `MaxArchiveBytes`), renames on success, then sets `creating` and calls `runtime.Templates.CreateTemplate` with the file. The builder environment is destroyed.
  5. `verifying`: provision a `verify` environment from the new template (`GHRM_MODE=selftest`, `GHRM_SELFTEST_BLOCKED`). `ReceiveSelfTest` → `CompareReports` against `PublishedReport` → store the report; the verify environment is destroyed.
  6. All checks OK → `ready`; then, when `AutoActivate`, no version is pinned and `Unexpected == 0`, activate it. Otherwise it stays `ready` with an event explaining why.
  7. Retention: keep the active version plus the newest `Keep-1` others; `retire` older ones and delete them once `TemplateInUse` is false (checked on every run of the loop).
  8. Any error or timeout (`BuildTimeout`, `VerifyTimeout`, builder powered off without `build_finished`) → `failed` with stage and reason, temporary guests destroyed, partial archive and half-created template removed. The active template never changes.
- The controller: `Kind` filters (scaler and reaper only act on `job` environments), `Spec.Template` from `TemplateSource.Active` (fallback: `proxmox.template_vmid`), `TemplateVMID` recorded on each environment, and `AgentEvent` forwards events of `build`/`verify` environments to the service. `serve.go` wires it all and runs `Recover` before the loops.
- Bootstrap: when the store has no template, the configured `proxmox.template_vmid` is registered once as an `active` version with `trigger="bootstrap"` (no archive; never deleted by retention).
- `CompareReports`: parse both reports as the JSON tree produced by `generate-software-report.sh` (nodes with `NodeType`, `Title`, `Version`/`Content`), flatten to `title → version`, diff; names listed by the layer (`Docker`, `Docker Compose`, `Docker-Buildx`, `systemd`, `GitHub Actions Runner`, `ghrm-agent`) are `Explained`.

- [ ] Failing tests (fake runtime, fake releases, fake clock):
  - happy path to `active`, with events in order and the builder/verify environments destroyed;
  - `ErrBuildRunning` for a second build; failure at each stage (builder poweroff, bad SHA, oversized archive, create error, self-test check failure, verify timeout) ends `failed`, cleans up, keeps the previous active;
  - unexpected differences keep the version `ready` (not active); a pinned version blocks auto-activation; `Activate` swaps active and previous;
  - retention retires the third-newest and deletes it only when not in use, and never deletes the bootstrap version;
  - `Recover` fails a version stuck in `building` and destroys its guest;
  - the checker builds when the slim release, runner version or layer version differs from the active version, and not otherwise;
  - `CompareReports` table tests (missing tool, version change, layer additions explained, malformed JSON is an error);
  - config defaults and validation (template range must not overlap `proxmox.vmid_range`; `keep >= 2`);
  - controller: environments clone from the active template and record `TemplateVMID`; build environments are invisible to scaling and reaping.
- [ ] Run, implement, run `go test -race ./...` (PASS). Commit `feat(template): build, verify, activate and retain templates; release checker`.

### Task 8: API and demo

**Files:**
- Create: `internal/api/templates.go`, tests in `internal/api/api_test.go`
- Modify: `internal/api/api.go` (Deps gains `Templates TemplateService`), `internal/demo/demo.go` (simulated builds through the fake runtime: the demo agent plays the build and self-test modes with log lines and a canned report), `cmd/ghrm/demo.go`

**Interfaces — Produces:**
- `GET /api/v1/templates` → `{templates: [Template...], building: bool}`; `Template` API view adds `active`, `in_use` and the parsed `report`.
- `GET /api/v1/templates/{id}`.
- `POST /api/v1/templates/build` (admin) → 202 with the new version, 409 when a build runs.
- `POST /api/v1/templates/{id}/activate`, `/pin`, `/unpin` (admin) → 202/204; 409 when the version is not `ready`/`active`.
- Events: `template.build_started`, `template.step`, `template.ready`, `template.activated`, `template.failed`, `template.retired`, `template.deleted`, `template.check` — the UI invalidates `["templates"]` on `template.*`.
- [ ] Failing tests (seeded store and a fake service): list and get shapes, admin required (401/403), 409 paths, events invalidation key.
- [ ] Run, implement, run (PASS). Regenerate the UI client (`make web-api`). Commit `feat(api): template versions, build, activate and pin`.

### Task 9: Templates UI

**Files:**
- Modify: `web/src/pages/templates.tsx`, `web/src/lib/event-stream.ts` (`template` family → `["templates"]`), `web/src/api/queries.ts`, `web/src/router.tsx` (`/templates/$id`)
- Create: `web/src/pages/template-detail.tsx`, `web/src/components/admin-action.tsx` (admin-token flow extracted from `destroy-environment.tsx` and reused), tests

**Behaviour:**
- List (Kumo `Table` in a `LayerCard`): version id, slim release, runner version, layer version, state badge, size, created, badges for `active`, `pinned`, `in use`; actions menu (`DropdownMenu`): activate (roll back), pin/unpin. A primary "Build now" button in the header (admin), disabled with a tooltip while a build runs. Empty state when only the bootstrap template exists, explaining the first build.
- Detail tabs: **Build** (timeline of the version's events and the live `build` log of the builder environment via the existing `LiveLog`), **Verification** (checks table and the `selftest` log), **Fidelity** (differences table: kind, name, expected, actual, explained badge; a success banner when there are no unexpected differences), **Details** (VMID, volume, SHA-256, size, trigger, failure).
- Unknown states render neutral (as everywhere).
- [ ] Failing component tests: list renders versions with badges; build button posts with the admin token and shows a toast; 409 shows "a build is already running"; detail shows the fidelity differences with unexpected ones highlighted; empty state.
- [ ] Run, implement, run (`pnpm lint && pnpm typecheck && pnpm test`, PASS). Extend the Playwright suite: the demo builds a template live (trigger "Build now" with the demo admin token, see it go `building → verifying → active` without a reload). Commit `feat(web): templates page with builds, fidelity reports, pin and roll back`.

### Task 10: Real build on the development Proxmox, docs and deployment

- Grant the restricted token what template builds need, on the dev host (document the exact commands in `docs/development.md`): `Datastore.AllocateTemplate` and `Datastore.Audit` on `/storage/local`; the existing role already has `VM.Allocate`, `VM.Config.*`, `Datastore.AllocateSpace` on `local-lvm`, `SDN.Use`. Create the `GhrmTemplates` role if a separate role reads better.
- Configure `templates:` on the dev control plane (LXC 310): range 950–958 (950 = bootstrap), `selftest_blocked: [<LAN gateway>, <Proxmox host>]`, `agent_path`.
- Deploy `ghrm` and `ghrm-agent`; trigger a build from the UI; watch it end `active`; run one real job on the new template if a workflow can be triggered (needs the user — see the M3 note); record timings and the fidelity differences in the ledger.
- Update `docs/architecture.md`: §7 template pipeline (implemented, with the builder-from-active-template ruling), the status table, the system overview (template service, builder environments), the code map (`internal/template`, `template/layer`), and a state diagram for template versions.
- Update the README (templates in "How it works") and `deploy/examples/ghrm.example.yaml`.
- Retire `deploy/proxmox/dev-template.sh` to "bootstrap only" in its header comment (it remains the way to create the very first template until the M5 installer does it).

## Self-review notes (plan author)

- **Ruling against spec §8.3 step 1:** the builder is cloned from the **active template**, not from the stock Proxmox Ubuntu template. The Proxmox API cannot run commands inside a guest, and the job network blocks inbound connections, so a stock container has no way to receive the build instructions. The active template already has Docker, systemd and the agent, and the agent's build mode reuses the authenticated ingest channel. The very first template still comes from `dev-template.sh` (M2) or the M5 installer. Cost if wrong: a bootstrap template is required before the first build.
- Spec §8.4 "canary workflow" is optional and out of scope here.
- The plan describes interfaces, behaviour and tests rather than full code for each step, like the M1–M3 plans of this repository; the executor writes the tests first from these descriptions.
