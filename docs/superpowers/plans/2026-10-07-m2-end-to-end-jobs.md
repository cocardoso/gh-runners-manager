# M2 — End-to-End Jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run real GitHub Actions jobs end to end without a UI. M2 is complete when a workflow targeting a ghrm scale set runs in an ephemeral LXC, its logs and lifecycle events reach the control plane in real time, and the environment is destroyed afterwards.

**Architecture:** `ghrm serve` wires the M1 runtime to:
- a SQLite store and an event bus;
- one `actions/scaleset` listener per configured scale set;
- a controller that turns desired counts into environments through the M1 scheduler;
- an HTTPS ingest that receives agent frames;
- a reaper;
- a read-only REST API with SSE streams.

`ghrm-agent` runs inside every job LXC, starts the runner from the injected JIT config, and streams logs, events and metrics to the ingest.

**Tech Stack:** Go 1.27, `modernc.org/sqlite`, `github.com/pressly/goose/v3`, `github.com/actions/scaleset` v0.4.0, `github.com/danielgtaylor/huma/v2` (stdlib adapter), standard library `crypto/tls` and `net/http`.

**Spec:** `docs/superpowers/specs/2026-10-07-gh-runners-manager-design.md` (§4–§7, §9, §10.5)

## Plan format note

From M2 on, plans specify files, interfaces, test cases and steps, but not full code: the same agent writes the plan and executes it, and full code in the plan doubled the work in M1. The test cases named here are binding. Each one is written first and watched failing (TDD), as in M1.

## Global Constraints

- All M1 Global Constraints still apply (English only, no Claude attribution, `CGO_ENABLED=0`, standard `testing`, token-only Proxmox, VMID range, tags, timeouts).
- **SQLite access is hand-written** with `database/sql` and the `modernc.org/sqlite` driver. Migrations use goose with an embedded FS. Ruling: sqlc's code generator needs cgo to build, and the query surface is small.
- Times are stored as Unix milliseconds (INTEGER). Event sequence numbers are SQLite `INTEGER PRIMARY KEY AUTOINCREMENT`.
- Events are persisted before they are published (spec §7).
- Agent bootstrap variables:
  - `GHRM_JITCONFIG`
  - `GHRM_ENVIRONMENT_ID`
  - `GHRM_INGEST_URL`
  - `GHRM_INGEST_TOKEN`
  - `GHRM_INGEST_FINGERPRINT`
- Per-environment ingest tokens are 32 random bytes, hex-encoded. Only `sha256(token)` is stored.
- Log streams:
  - `control-plane`: controller decisions;
  - `runtime`: runtime operations;
  - `agent`: messages from the agent;
  - `runner`: runner stdout/stderr and `_diag/Runner_*.log`, `_diag/Worker_*.log`;
  - `job`: `_diag/pages/*.log`;
  - `metrics`: JSON samples.
- `docs/architecture.md` is updated in the same change as any code that alters components, flows or states.

## Review Focus

1. **Duplicate or replayed agent frames** (agent retries after a timeout) must not duplicate log lines. Dedup by `(environment, stream, seq)`. Covered in Task 5.
2. **An agent presenting another environment's token**, or none, must be rejected with 401, and nothing may be written. Covered in Task 5.
3. **Control-plane restart while environments are live** must neither leak them nor create duplicates: state is rebuilt from the store and the runtime. Covered in Task 7 (reaper) and Task 6 (controller startup reconcile).
4. **Scale set message storms** (desired count jumps, JobCompleted for an unknown runner) must not create more environments than the scheduler allows, and must not crash. Covered in Task 6.
5. **The runner exits without the agent reporting it** (agent killed, guest powered off): the environment must still reach `destroyed` through timeouts or the runtime status. Covered in Task 7.

---

### Task 1: Store (SQLite, migrations, repositories)

**Files:** `internal/store/store.go`, `internal/store/migrations/0001_init.sql`, `internal/store/environments.go`, `internal/store/jobs.go`, `internal/store/events.go`, `internal/store/logstreams.go`, `internal/store/scalesets.go`, tests beside each.

**Interfaces (produces):**
- `store.Open(ctx, path string) (*store.Store, error)`: WAL, `busy_timeout=5000`, `foreign_keys=1`; runs migrations. `(*Store).Close() error`.
- `store.Environment{ID, ScaleSet, State string; RuntimeRef, RunnerName string; RunnerID int64; IP, TokenHash, FailureStage, FailureReason, JobID string; ExitCode *int; MemoryMB int; CreatedAt, UpdatedAt, StateChangedAt time.Time}`
- `(*Store) CreateEnvironment(ctx, e Environment) error`, `GetEnvironment(ctx, id) (Environment, error)` (`store.ErrNotFound`), `ListEnvironments(ctx, store.EnvironmentFilter{States []string; ScaleSet string; Limit int}) ([]Environment, error)` (newest first)
- `(*Store) TransitionEnvironment(ctx, id string, from []string, to string, mutate func(*Environment)) (Environment, error)`: compare-and-set in one transaction. `store.ErrConflict` if the current state is not in `from`. It validates with `environment.CanTransition`.
- `(*Store) UpdateEnvironment(ctx, id string, mutate func(*Environment)) (Environment, error)`: non-state fields.
- `store.Job{ID, ScaleSet, Repository, Owner, WorkflowRef, DisplayName, EventName, RunnerName, EnvironmentID, Status, Result string; RunID int64; QueuedAt, StartedAt, FinishedAt time.Time}` with `UpsertJob(ctx, j Job) error` (merges non-zero fields), `GetJob`, `ListJobs(ctx, store.JobFilter{ScaleSet, Status string; Limit int})`
- `store.Event{Seq int64; Time time.Time; Kind, Level, Message, ScaleSet, EnvironmentID, JobID string; Data map[string]any}` with `AppendEvent(ctx, e Event) (Event, error)` (assigns Seq) and `ListEvents(ctx, store.EventFilter{AfterSeq int64; EnvironmentID, JobID string; Limit int}) ([]Event, error)` (ascending)
- `store.LogStream{EnvironmentID, Stream, Path string; LastSeq, Bytes, Lines int64; FirstAt, LastAt time.Time}` with `GetLogStream`, `UpsertLogStream(ctx, s LogStream) error`, `ListLogStreams(ctx, envID) ([]LogStream, error)`
- `store.ScaleSetRecord{Name string; GitHubID int; URL string; UpdatedAt time.Time}` with `GetScaleSet` / `PutScaleSet`

**Tests (binding):**
- `TestOpenMigratesAndReopens`: open, close, reopen the same file; the schema version is unchanged.
- `TestEnvironmentCRUDAndFilter`: create 3 environments in different states; filter by states and by scale set; newest first.
- `TestTransitionEnvironmentCompareAndSet`: an allowed transition updates the state and `StateChangedAt`. A wrong `from` gives `ErrConflict`. A transition the state machine forbids gives an error. Two concurrent transitions from the same state: exactly one wins.
- `TestUpsertJobMergesFields`: insert with repository; upsert with runner name only; both are kept.
- `TestAppendEventAssignsIncreasingSeqAndRoundTripsData`: `ListEvents` with `AfterSeq` returns only newer events; JSON data round-trips.
- `TestLogStreamUpsert`.

- [ ] Steps: add dependencies (`modernc.org/sqlite`, `github.com/pressly/goose/v3`); write the migration; then, per test: write it, watch it fail, implement, pass. Commit: `feat(store): add the SQLite store with migrations`.

### Task 2: Event bus and recorder

**Files:** `internal/events/bus.go`, `internal/events/recorder.go`, tests.

**Interfaces:**
- `events.Bus`: `NewBus() *Bus`, `(*Bus) Publish(store.Event)`, `(*Bus) Subscribe(buffer int) *events.Subscription` where `Subscription{C <-chan store.Event}` has `Lagged() bool` and `Close()`. A full subscriber channel marks it lagged and drops the event; the consumer resyncs from the store.
- `events.Recorder`: `NewRecorder(s *store.Store, b *Bus, now func() time.Time) *Recorder`; `(*Recorder) Record(ctx, store.Event) (store.Event, error)` persists then publishes. Convenience: `Info(ctx, kind, msg string, refs events.Refs, data map[string]any)`, `Warn`, `Error`. `events.Refs{ScaleSet, EnvironmentID, JobID string}`.

**Tests:**
- `TestBusFanOut`: two subscribers receive the same event.
- `TestSlowSubscriberIsMarkedLaggedNotBlocking`: a buffer of 1 and 3 publishes; `Publish` never blocks; `Lagged()` is true.
- `TestRecorderPersistsBeforePublish`: on receipt, the event is already readable from the store with the same Seq.
- `TestCloseStopsDelivery`.

- [ ] Commit: `feat(events): add the in-process event bus and recorder`.

### Task 3: Log store

**Files:** `internal/logs/logs.go`, tests.

**Interfaces:**
- `logs.Store`: `New(dir string, db *store.Store) *logs.Store`
- `(*logs.Store) Append(ctx, envID, stream string, lines []logs.Line) (accepted int, err error)` with `logs.Line{Seq int64; Time time.Time; Text string}`. It drops lines with `Seq <= LastSeq`, appends `<RFC3339Nano>\t<text>\n` to `dir/<envID>/<stream>.log`, and updates the `log_streams` row.
- `(*logs.Store) Read(ctx, envID, stream string, offset int64, max int) ([]logs.Entry, next int64, err error)` with `logs.Entry{Offset int64; Time time.Time; Text string}`.
- `(*logs.Store) Follow(ctx, envID, stream string, offset int64) <-chan logs.Entry`: emits existing lines from `offset`, then new ones as they are appended (an in-process notifier per stream, no polling). The channel closes when ctx ends.
- `logs.ValidStream(name string) bool` for the six stream names.

**Tests:**
- `TestAppendDedupsBySeq`: append seq 1–3, then 2–5; the file has 5 lines.
- `TestReadWithOffsets`.
- `TestFollowDeliversBacklogThenLive`.
- `TestRejectsUnknownStreamAndPathTraversal`: an environment ID with `..` or `/`, or an unknown stream, is rejected.

- [ ] Commit: `feat(logs): add the per-environment log store with follow`.

### Task 4: Configuration for serving

**Files:** modify `internal/config/config.go` and its test; update `deploy/examples/ghrm.example.yaml`.

**Adds:**
- `DataDir string` (`data_dir`, default `/var/lib/ghrm`)
- `Listen string` (`listen`, default `127.0.0.1:8080`)
- `AdminTokenFile string` (`admin_token_file`, optional)
- `Ingest{Listen, AdvertiseURL string}`: both required for `serve`; the advertise URL must be https.
- `GitHub.Credentials []{Name, TokenFile string}`: token from the file, or `GHRM_GITHUB_TOKEN_<NAME>` (name upper-cased, `-` to `_`).
- `Capacity{MaxEnvironments, MemoryBudgetMB, MemoryMarginMB int; MaxDiskPercent float64}`. Defaults: 4, 16384, 4096, 85.
- `ScaleSets []{Name, URL, Credential, RunnerGroup string; Labels []string; MaxConcurrent, Cores, MemoryMB, KeepOnFailureMinutes int}`. Defaults: runner group `default`, max 2, cores 2, memory 4096.

Validation:
- Scale set names must be unique and match `^[a-z0-9][a-z0-9-]{0,62}$`.
- The URL must be `https://github.com/<owner>[/<repo>]`.
- The credential must exist.

`config.Load` keeps working for `smoke` (serve-only fields are validated by `(*Config) ValidateServe() error`).

**Tests:**
- `TestServeDefaults`.
- `TestServeValidationErrors`: duplicate name, bad URL, unknown credential, missing ingest, http advertise URL.
- `TestGitHubTokenFromEnv`.

- [ ] Commit: `feat(config): add serve, ingest, GitHub, capacity and scale set settings`.

### Task 5: Ingest (TLS, tokens, frames)

**Files:** `internal/ingest/cert.go`, `internal/ingest/server.go`, `internal/ingest/protocol.go` (shared with the agent), tests.

**Protocol (`internal/ingest/protocol.go`, imported by the agent):**
- `POST /ingest/v1/frames`, `Authorization: Bearer <token>`, `Content-Type: application/x-ndjson`; one JSON frame per line:
  - `{"type":"log","stream":"runner","seq":12,"time":"…","text":"…"}`
  - `{"type":"event","name":"hello|runner_started|runner_online|job_started|job_finished|runner_exited|shutdown","time":"…","data":{…}}`
  - `{"type":"metric","seq":3,"time":"…","cpu_usec":…,"mem_bytes":…}` (stored on the `metrics` stream as JSON text)
- Response: `{"accepted":n}`. 401 for a bad token; 400 for a malformed frame (rejected, never partially written); 413 for a body over 4 MiB.

**Interfaces:**
- `ingest.LoadOrCreateCert(dir string, hosts []string) (tls.Certificate, fingerprint string, err error)`: ECDSA P-256, valid for 10 years, SANs from the advertise URL host, stored `0600`.
- `ingest.TokenResolver` interface `Resolve(ctx, tokenHash string) (envID string, ok bool)`, implemented by the controller over the store.
- `ingest.EventSink` interface `AgentEvent(ctx, envID string, name string, at time.Time, data map[string]any)`, implemented by the controller.
- `ingest.NewServer(resolver, sink, logStore *logs.Store, recorder *events.Recorder) http.Handler`.
- `ingest.HashToken(token string) string`, `ingest.NewToken() (string, error)`.

**Tests:**
- `TestFramesWithValidTokenAreStored`.
- `TestWrongTokenIsRejectedAndNothingWritten` (Review Focus #2).
- `TestReplayedFramesAreDeduplicated` (Review Focus #1).
- `TestMalformedBatchRejectedAtomically`.
- `TestOversizedBodyRejected`.
- `TestEventsReachTheSink`.
- `TestCertificateIsReusedAcrossRestarts`: same fingerprint on the second load.

- [ ] Commit: `feat(ingest): add the agent ingest with per-environment tokens`.

### Task 6: GitHub scale sets and the controller

**Files:** `internal/github/github.go` (adapter over `actions/scaleset`), `internal/controller/controller.go`, `internal/controller/scaler.go`, `internal/controller/provision.go`, tests with fakes.

**Interfaces:**
- `controller.GitHub` interface (implemented by `internal/github`, faked in tests):
  - `EnsureScaleSet(ctx, cfg config.ScaleSet) (id int, err error)`: get by name in the runner group, else create with labels and `DisableUpdate: true`.
  - `GenerateJIT(ctx, scaleSetID int, runnerName string) (runnerID int64, encodedJIT string, err error)`
  - `RemoveRunner(ctx, runnerID int64) error`: a 404 is nil.
  - `Listen(ctx, scaleSetID, maxRunners int, scaler listener.Scaler) error`: session client + `listener.Run`.
- `controller.New(controller.Deps{Store, Recorder, Runtime runtime.Runtime, GitHub, Logs *logs.Store, Config *config.Config, IngestURL, IngestFingerprint string, Now func() time.Time}) *Controller`
- `(*Controller) Scaler(scaleSet string) listener.Scaler`: `HandleDesiredRunnerCount` records the desired count, triggers a reconcile and returns the live count. `HandleJobStarted` / `HandleJobCompleted` upsert the job, map the runner name to an environment, and transition it (`idle → running`, `* → completing`).
- `(*Controller) Reconcile(ctx) error`:
  - builds `scheduler.Demand` per scale set from the desired count and the live environments in the store;
  - builds `scheduler.Capacity` from config, `runtime.Capacity` and the store;
  - calls `scheduler.Decide`, then provisions `Create[...]` environments concurrently.
  - Waiting reasons are recorded as events only when they change.
- Provisioning one environment:
  1. insert `pending` with a new ID and token hash;
  2. `provisioning`;
  3. `GenerateJIT` (runner name `ghrm-<last 12 chars of id>`);
  4. `runtime.Create` with the env vars;
  5. `booting`;
  6. `runtime.Start`.
  On failure at any step: `failed` with stage and reason, then `destroying` → `runtime.Destroy` → `RemoveRunner` → `destroyed`. Every step is recorded on the `control-plane` stream and as events.
- `AgentEvent` (implements `ingest.EventSink`):
  - `hello` → `connected`;
  - `runner_online` → `idle`;
  - `job_started` → `running`;
  - `runner_exited` → `completing`, with the exit code.
  Unknown or out-of-order events are recorded but never panic.
- Teardown: a `completing` environment is destroyed once the runtime reports it not running, or 30 s after entering `completing`. Keep-on-failure: a `failed` environment of a scale set with `KeepOnFailureMinutes > 0` is destroyed only after that window.
- `(*Controller) ResolveToken` implements `ingest.TokenResolver` (live environments only).

**Tests (fakes: `runtimetest.Fake`, a fake GitHub, a temporary store):**
- `TestDesiredCountProvisionsEnvironments`: desired 2 gives 2 environments in `booting`; the runtime has 2 started guests; each has the 5 bootstrap env vars and a unique token.
- `TestCapacityLimitsProvisioning` (Review Focus #4): max environments 1, desired 5 → one environment and a `global_limit` waiting event.
- `TestProvisionFailureIsCleanedUp`: runtime `CreateErr` → environment `failed` (stage `create`), then destroyed; the JIT runner is removed.
- `TestAgentEventsDriveStates`: `hello` → connected; `runner_online` → idle; `job_started` → running; `runner_exited` → completing.
- `TestJobMessagesUpsertJobsAndTolerateUnknownRunners` (Review Focus #4).
- `TestCompletingEnvironmentIsDestroyed`.
- `TestKeepOnFailureDelaysDestroy`.
- `TestStartupAdoptsLiveEnvironments` (Review Focus #3): store has `running` environment X; the runtime has X → no new environment, X kept.

- [ ] Commit: `feat(controller): provision environments from scale set demand`.

### Task 7: Reaper

**Files:** `internal/controller/reaper.go`, tests.

**Behaviour:** every 30 s, and once at startup:
1. Runtime environments with no store row, or whose row is `destroyed`, are destroyed (orphans).
2. Live store environments whose runtime guest is gone are marked `failed` (`runtime_gone`), then `destroyed`.
3. Expired states (`environment.DefaultTimeouts`) become `failed` (`timeout:<state>`), then go through destroy.
4. A `booting`/`connected`/`idle` environment whose guest is not running (the guest powered off without reporting) goes to `completing`.

**Tests:**
- `TestReaperDestroysOrphans`.
- `TestReaperMarksGoneEnvironments`.
- `TestReaperEnforcesTimeouts` (Review Focus #5).
- `TestReaperHandlesSilentPowerOff` (Review Focus #5).

- [ ] Commit: `feat(controller): add the reaper`.

### Task 8: REST API and SSE

**Files:** `internal/api/api.go`, `internal/api/sse.go`, tests.

**Endpoints (`/api/v1`):**
- `GET /scale-sets`: config, GitHub ID, desired/live counts, waiting reason.
- `GET /environments?state=&scale_set=&limit=`
- `GET /environments/{id}`: includes its log streams.
- `GET /jobs?status=&scale_set=&limit=`
- `GET /jobs/{id}`
- `GET /events?after=&environment=&job=&limit=`
- `GET /events/stream`: SSE. `id:` is the sequence; honours `Last-Event-ID` and `?after=`. On lag, it resyncs from the store. Heartbeat comment every 15 s.
- `GET /environments/{id}/logs/{stream}?offset=&follow=`: `follow=true` gives SSE with `id:` = offset; otherwise JSON `{entries, next}`.
- `POST /environments/{id}/destroy`: requires `Authorization: Bearer <admin token>` when configured, else 403. It is recorded as an audit event.
- `GET /healthz`, `GET /readyz` (store ping + runtime capacity call), `GET /api/openapi.json` (huma).

**Tests (httptest):**
- `TestListAndGetEndpoints`.
- `TestEventStreamResumesFromLastEventID`.
- `TestLogFollowStreamsNewLines`.
- `TestDestroyRequiresAdminToken`.
- `TestOpenAPIDocumentIsServed`.

- [ ] Commit: `feat(api): add the read-only REST API and SSE streams`.

### Task 9: `ghrm serve`

**Files:** `cmd/ghrm/serve.go`, test.

**Behaviour:**
1. Load and validate the config; open the store under `data_dir`; load or create the ingest certificate.
2. Build the runtime, controller and reaper.
3. For each scale set: `EnsureScaleSet`, then start `Listen` in a goroutine, restarting it with backoff if it fails.
4. Start the ingest TLS server on `ingest.listen` and the API server on `listen`.
5. On SIGTERM: stop the listeners, wait up to 30 s for in-flight provisioning, close the store. Live environments are kept; they are adopted at the next start.

**Test:** `TestServeRejectsInvalidConfig` (exit code 1 and a message). The full wiring is exercised by Task 11.

- [ ] Commit: `feat: add ghrm serve`.

### Task 10: `ghrm-agent`

**Files:** `cmd/ghrm-agent/main.go`, `internal/agent/bootstrap.go`, `internal/agent/client.go`, `internal/agent/tail.go`, `internal/agent/runner.go`, `internal/agent/metrics.go`, tests.

**Behaviour:**
- **Bootstrap.** Parse `/proc/1/environ`; the path is configurable for tests. Without `GHRM_JITCONFIG`, exit 0 (template boot).
- **Client.** Batches frames every 250 ms (or every 500 frames). TLS is pinned to `GHRM_INGEST_FINGERPRINT`. Retries with backoff while keeping order. An in-memory queue is capped at 100k frames; when the cap is hit, the oldest log frames are dropped and a `frames_dropped` event is sent. Ruling: in-memory instead of the spec's disk spool for M2. The spool is deferred, and the cap makes loss explicit.
- **Runner.**
  1. Run `run.sh --jitconfig <jit>` as the `runner` user (uid/gid looked up from `/etc/passwd`), working dir `/home/runner/actions-runner`.
  2. stdout/stderr go to the `runner` stream.
  3. Events: `runner_online` on a line containing `Listening for Jobs`, `job_started` on `Running job:`, `runner_exited` with the exit code.
- **Tail.** Follow `_diag/Runner_*.log` and `_diag/Worker_*.log` into `runner`, and `_diag/pages/*.log` into `job`. New files are discovered every second; partial lines wait for the newline.
- **Metrics.** Every 5 s, read cgroup v2 `cpu.stat` (`usage_usec`) and `memory.current` from `/sys/fs/cgroup`, into the `metrics` stream.
- **Lifecycle.**
  1. `hello` (agent version, hostname, IPv4) before starting the runner.
  2. After `runner_exited`: stop the tails after a final read, flush (up to 30 s), send `shutdown`, then run `systemctl poweroff`. The command is configurable; tests use a no-op.

**Tests:**
- `TestParseEnviron`.
- `TestClientPinsFingerprintAndRetries` (against an httptest TLS server: the wrong fingerprint is refused; a 503 then a 200 delivers in order).
- `TestTailFollowsNewFilesAndPartialLines`.
- `TestRunnerEventsFromOutput` (a fake `run.sh` script prints the markers and exits 3 → events, exit code 3).
- `TestMetricsReadsCgroupFiles` (a fake cgroup dir).

- [ ] Commit: `feat(agent): add ghrm-agent`.

### Task 11: Deployment for end-to-end (homelab) and verification

**Files:**
- `deploy/proxmox/dev-template.sh`: builds a template from an existing ghrm template clone. It installs `ghrm-agent` and its systemd unit and removes the M1 spike entrypoint. The M4 builder replaces this script.
- `deploy/systemd/ghrm.service`, `deploy/systemd/ghrm-agent.service`.
- `docs/development.md` (end-to-end section).
- `docs/architecture.md` (status table and diagrams).

**Steps** (operator pre-authorized: "do everything you can"):
1. Add the ingest exception rule to the job security group: OUT ACCEPT TCP to `10.50.0.2:8443`, placed before the RFC 1918 drop.
2. Create the control-plane LXC (Debian 13, VMID outside the environment range):
   - NIC 1 on the LAN with a free address;
   - NIC 2 on `jobnet` with `10.50.0.2`;
   - install `ghrm` and its service, config and secrets.
3. Build a new template with the agent from template 949 and point `template_vmid` to it.
4. Configure a scale set for the test repository, using the existing fine-grained PAT. Push a temporary branch whose workflow uses `runs-on: <scale set name>` and runs a small job plus a `services:` job.
5. Verify:
   - the job succeeds;
   - the events show `pending → … → destroyed`;
   - the `runner` and `job` logs are readable through `/api/v1/environments/{id}/logs/job?follow=true`;
   - no leftover guests or runners.

   Then delete the branch.
6. Update `docs/architecture.md`: M2 components move to implemented; the sequence diagram gains the agent/ingest steps.

- [ ] Commit(s): `feat(deploy): add development deployment assets`, `docs: update architecture for M2`.
