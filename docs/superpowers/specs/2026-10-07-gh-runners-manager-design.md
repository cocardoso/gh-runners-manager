# gh-runners-manager — Design

- **Status:** Draft for review
- **Date:** 2026-10-07

## 1. Summary

gh-runners-manager (`ghrm`) runs GitHub Actions jobs on self-hosted infrastructure with **one fresh, isolated environment per job**. The environment matches GitHub's hosted Ubuntu image as closely as practical, and it is destroyed when the job ends. A real-time web UI shows everything that happens, down to the logs of each lifecycle stage of each runner.

The first runtime is **Proxmox VE**. Each job runs in an unprivileged LXC container that is cloned from a template, attached to an isolated network, and destroyed after the job. The design keeps the runtime behind an interface so that other runtimes (plain Docker hosts, cloud VMs) can be added later without changing the core.

The target audience is homelabs and small teams that want hosted-runner behaviour on their own hardware without Kubernetes.

## 2. Goals and non-goals

### Goals

1. **Per-job isolation.** No state carries over between jobs: no files, processes, caches, Docker images or credentials. A job cannot reach the operator's LAN or the hypervisor.
2. **Fidelity with GitHub-hosted runners.** A workflow that passes on `ubuntu-*` hosted runners should pass unchanged, apart from `runs-on`. This includes jobs that use Docker: `services:`, `container:`, `docker compose`, buildx and Docker container actions.
3. **Observability.** A real-time UI covers queues, environments, jobs, steps and logs for every lifecycle stage. The logs are detailed enough to debug a failure without SSH.
4. **Low maintenance.** The tool list of the job image is maintained by GitHub, not by this project.
5. **Simple operation.** One binary, one SQLite database, an idempotent installer for Proxmox and a Docker Compose setup for local use.

### Non-goals (for now)

- Kubernetes support.
- Windows or macOS runners.
- Cloud runtimes (AWS, GCP, Azure). The runtime interface allows them later; none is built now.
- A "full" image equivalent to `ubuntu-24.04`, and per-project custom images. Only the `ubuntu-slim`-based profile is in scope.
- Multi-node Proxmox clusters. Single node first; the design does not preclude clusters.
- GitHub App authentication. Fine-grained PATs first, behind an interface that admits an App later.

## 3. Background: validated findings

An end-to-end spike was run on a single-node Proxmox VE 9.2 host before this design was written. The real CI workflows of two projects ran in ephemeral LXC containers. Together they covered MySQL and PostgreSQL service containers, Playwright in `container:`, `docker compose up --build` with bind mounts, multi-image buildx builds and Docker container actions.

**What worked**

- Docker Engine runs inside **unprivileged** LXC containers with `nesting=1,keyctl=1` (overlayfs storage driver, cgroup v2). Every Docker-based job type above passed, including buildx with the `docker-container` driver.
- Lifecycle: `generate-jitconfig` → linked clone of a template LXC → inject the JIT config through the container's runtime environment (`pct set --env`) → start → the runner executes one job → the guest powers off → the control plane destroys it.
- **Speed.** A linked clone takes about 1 s. The runner is online about 5 s after the guest starts, so it takes about 10 s to go from "job queued" to "runner online" without a warm pool. Template size does not affect per-job cost.
- A cloned LXC inherits the template's firewall configuration and security group.
- A Proxmox SDN "Simple" zone with SNAT and dnsmasq DHCP provides an isolated job network. A security group that drops RFC 1918 and link-local destinations keeps jobs off the LAN and off the hypervisor.
- Peak memory per job was 0.1–2.0 GiB for typical web-application CI.
- Job durations were within about 10–15% of GitHub-hosted runners, except a buildx-heavy job: 7.4 min against 3.1 min, because every job starts with a cold Docker cache.

**Fidelity gaps found, all caused by a hand-written template**

1. `LANG` must be `C.UTF-8`, as on hosted runners. With `en_US.UTF-8`, `sort` orders lines differently and a real test failed.
2. Hosted images ship Node.js 22 on `PATH`. A job that calls `node` without `setup-node` failed.
3. When an LXC has no `nameserver` configured, Proxmox copies the host's DNS settings into the guest. On an isolated network that DNS server is unreachable, so the template must set `nameserver` explicitly.

These gaps are why the design moves the tool list to GitHub's own image recipe (Section 8).

**Operational findings**

- `pve-firewall` applies rules for a newly created guest on its next compile cycle, which takes about 10 s. Starting a guest before that cycle leaves an unfiltered window. Section 10 addresses this.
- The job network is IPv4-only, so the image forces IPv4 for apt and similar tools.

## 4. Architecture

```
                     GitHub (Runner Scale Set API · JIT config · REST)
                                   ▲  outbound long-poll only
┌──────────────────────── control plane: "ghrm" ─────────────────────────┐
│ ghrm (single Go binary)                                                 │
│  ├─ Listener    one per scale set (github.com/actions/scaleset)         │
│  ├─ Scheduler   desired vs actual environments, capacity limits         │
│  ├─ Runtime     interface; first implementation: proxmox-lxc            │
│  ├─ Reaper      reconciles DB / runtime / GitHub, enforces timeouts     │
│  ├─ Templates   builds, verifies and rolls out template versions        │
│  ├─ Store       SQLite (state, events) + log files                      │
│  ├─ Ingest      receives events and logs from agents (TLS, per-env token)│
│  └─ API         REST (OpenAPI) + SSE + embedded web UI                  │
│ Network: LAN interface (UI, hypervisor API) · job-network interface (ingest only) │
└──────────────────────────────────────────────────────────────────────────┘
        │ Proxmox API                               ▲ events + logs
        ▼                                           │
┌──── job environment: unprivileged LXC, linked clone, ephemeral ──────┐
│ ghrm-agent: reads JIT config → runs the runner → streams logs,       │
│ steps, metrics and exit status → powers the guest off                │
└───────────────────────────────────────────────────────────────────────┘
```

### 4.1 Control plane (`ghrm`)

A single Go binary that embeds the web UI. Its subsystems are listed below.

- **Listener.** One per scale set. It uses the official `github.com/actions/scaleset` client, which is also the client used by Actions Runner Controller. It long-polls the scale set message queue and receives queue statistics and job lifecycle messages (assigned, started, completed). All traffic is outbound, so no inbound port or webhook is needed.
- **Scheduler.** Computes the desired number of environments per scale set and creates or releases environments within the capacity limits (Section 9).
- **Runtime.** An interface over the infrastructure that runs environments. The first implementation is `proxmox-lxc`.
- **Reaper.** Reconciles the three sources of truth (database, runtime, GitHub), removes orphans and enforces per-state timeouts.
- **Template manager.** Builds, verifies, activates and retires template versions (Section 8).
- **Store.** SQLite in WAL mode holds state and events. Raw logs are stored as files.
- **Ingest.** An HTTPS endpoint, reachable only from the job network, that receives events and logs from agents.
- **API.** A REST API described by OpenAPI, Server-Sent Events streams and the static web UI.

### 4.2 Runtime interface

```go
type Runtime interface {
    // Create provisions a stopped environment from a template, with the given
    // resources and runtime environment variables. It must be idempotent per spec.ID.
    Create(ctx context.Context, spec EnvironmentSpec) (EnvironmentRef, error)
    Start(ctx context.Context, ref EnvironmentRef) error
    Stop(ctx context.Context, ref EnvironmentRef) error
    Destroy(ctx context.Context, ref EnvironmentRef) error
    // List returns every environment this runtime owns, identified by tag,
    // so the reaper can find orphans.
    List(ctx context.Context) ([]EnvironmentStatus, error)
    Status(ctx context.Context, ref EnvironmentRef) (EnvironmentStatus, error)
    Capacity(ctx context.Context) (Capacity, error)
}
```

The template manager uses a separate, runtime-specific interface for importing and verifying templates.

**`proxmox-lxc` implementation**

| Concern | Approach |
|---|---|
| Create | Linked clone of the active template into a reserved VMID range (default `900–999`). Set memory, cores and disk; set `env` with the agent bootstrap variables; tag `ghrm` / `ghrm-env`. Store the environment ID in the description. |
| Network | The single NIC is attached to the job VNet with `firewall=1`. The clone inherits the security group from the template. |
| DNS | `nameserver` and `searchdomain` are set explicitly on the template and therefore on clones. |
| Firewall window | After `Create`, do not `Start` until the firewall rules for the new guest are confirmed applied (Section 10.3). |
| Destroy | `stop` if running, then `DELETE ...?purge=1&destroy-unreferenced-disks=1`. |
| List / orphans | All LXC guests with the `ghrm-env` tag. |
| Capacity | Node memory (total and available), thin pool data and metadata usage, and the count of `ghrm` guests. |

### 4.3 Agent (`ghrm-agent`)

A small static Go binary baked into the template and started by systemd at boot.

1. It reads its bootstrap from the container's runtime environment (`/proc/1/environ`): the JIT config, the environment ID, the ingest URL, the ingest certificate fingerprint and a per-environment token. If no bootstrap is present, for example during template verification, it runs in self-test mode instead.
2. It announces itself to the ingest (`hello`) and starts a heartbeat.
3. It starts the runner with `run.sh --jitconfig …` as the unprivileged `runner` user.
4. It streams the following sources to the ingest:
   - the runner's stdout and stderr;
   - the runner diagnostic logs (`_diag/Runner_*.log`, `_diag/Worker_*.log`);
   - the live job step output (`_diag/pages/*`);
   - cgroup CPU and memory samples every 5 s;
   - lifecycle markers: runner online, job started, job finished, exit code.
5. When the runner exits, it flushes the buffers, reports the final status and powers the guest off.

Transport: batched `POST` requests of NDJSON frames every ~250 ms, each frame carrying a per-stream sequence number. Frames are spooled to disk while the ingest is unreachable and retried. The ingest deduplicates frames by `(environment, stream, seq)`.

## 5. Environment lifecycle

```
pending → provisioning → booting → connected → idle → running → completing → destroying → destroyed
              │              │          │         │       │
              └──────────────┴──────────┴─────────┴───────┴──→ failed(stage, reason) → destroying
```

| State | Entered when | Default timeout |
|---|---|---|
| `pending` | The scheduler decides to create an environment | — |
| `provisioning` | The JIT config is generated, the clone is created and configured, and the firewall is confirmed | 2 min |
| `booting` | The guest is started | 2 min |
| `connected` | The agent's first `hello` arrives | 2 min |
| `idle` | The runner reports online | 10 min without a job |
| `running` | A job is assigned to this runner | 6 h |
| `completing` | The job finishes; the agent flushes logs and powers off | 5 min |
| `destroying` → `destroyed` | The guest is destroyed and the runner deregistered if still present | — |
| `failed` | Any stage fails or times out; records the stage and the reason | — |

**Keep on failure (debug).** This is an option on each scale set. When a job fails, the environment stays powered on for N minutes before it is destroyed, so the operator can inspect it from the Proxmox console. It counts against capacity and is highlighted in the UI.

## 6. Data model

| Entity | Purpose | Key fields |
|---|---|---|
| `scale_set` | One GitHub scale set (one repository or organization) | name, GitHub config URL, labels, CPU / memory / disk per environment, max concurrent, warm pool size (default 0), keep-on-failure minutes, paused |
| `template` | One built template version | slim image release tag, runner version, layer version, runtime ref (VMID), state (`building → verifying → active → retired`, or `failed`), sizes, fidelity report, build log ref |
| `environment` | One job environment (an LXC) | scale set, template, runtime ref, IP, runner name/ID, state, timestamp per transition, failure stage/reason, exit code, peak CPU/memory, agent token hash |
| `job` | One GitHub job | GitHub job/run IDs, repository, workflow, job name, branch, commit SHA, actor, URL, status, conclusion, queued/started/completed times, environment |
| `job_step` | Steps of a job, from the GitHub API | number, name, status, conclusion, timestamps |
| `event` | Append-only timeline of everything | monotonic sequence, timestamp, kind, level, message, references (scale set / environment / job / template), JSON payload |
| `log_stream` | Metadata of one raw log stream | environment or template build, stream name, file path, byte size, line count, first/last timestamps |

**Log streams of an environment**

- `control-plane`: scheduler and lifecycle decisions.
- `runtime`: hypervisor task logs (clone, start, destroy).
- `agent`: messages from the agent itself.
- `runner`: runner diagnostic logs.
- `job`: live step output.
- `metrics`: CPU and memory samples.

After a job completes, the official job log is downloaded from the GitHub API and stored as `job-github`, which is the authoritative copy.

**Retention** is configurable. Defaults: events and history for 30 days; logs for 7 days or until a total size cap (for example 5 GB) is reached, whichever comes first.

## 7. Real-time delivery

- **Agent → control plane:** batched NDJSON over HTTPS (Section 4.3).
- **Control plane → UI:** Server-Sent Events.
  - `GET /api/v1/events/stream` is the global event stream. It is resumable with `Last-Event-ID`, which maps to the event sequence, so a reconnecting client receives what it missed.
  - `GET /api/v1/environments/{id}/logs/{stream}?follow=true` is a per-log tail stream. It starts with a backlog (offset or last N lines), then delivers live lines.
- SSE was chosen over WebSockets because all real-time traffic flows from server to client, SSE reconnects natively, and it works through common proxies (for example a Cloudflare Tunnel) without extra configuration.
- **Step mapping.** The step list and statuses come from the GitHub API, polled while a job runs and refreshed on scale set job messages. The step content comes from the agent's `job` stream.
- **Internal bus.** An in-process publish/subscribe bus fans events out to SSE subscribers. Events are persisted before they are published, so the stream never shows something the database does not have.

## 8. Templates

### 8.1 Source of truth

GitHub publishes the recipes for its hosted images in [`actions/runner-images`](https://github.com/actions/runner-images). It publishes no prebuilt images for use outside its own infrastructure. One of the recipes, **`ubuntu-slim`**, is a plain Dockerfile that GitHub uses to run jobs in **unprivileged containers**. It reuses the same build scripts and toolset files as the full Ubuntu images and is rebuilt about weekly.

ghrm builds its templates **from that Dockerfile, unmodified**, at a pinned release tag such as `ubuntu-slim/20261005.17`. This gives ghrm GitHub's tool list, versions, environment variables and locale without this project maintaining them.

### 8.2 ghrm layer

A short Dockerfile that starts `FROM` the slim image adds only what an LXC job environment needs:

- `systemd` as init, because the slim image is designed for a container runtime and has no init;
- Docker Engine with the buildx and compose plugins (hosted Ubuntu images provide Docker; `ubuntu-slim` does not);
- the `actions/runner` release, with its SHA-256 verified, owned by an unprivileged `runner` user with passwordless sudo (as on hosted runners);
- `ghrm-agent` and its systemd unit;
- IPv4-only apt configuration and an explicit-DNS-friendly network setup;
- removal of `machine-id` and SSH host keys, so every clone gets unique ones.

### 8.3 Build pipeline

1. **Builder environment.** A temporary LXC on the job network, created from the stock Proxmox Ubuntu template, with Docker installed.
2. `git clone actions/runner-images` at the pinned tag, then `docker build` the official `ubuntu-slim` Dockerfile, then `docker build` the ghrm layer.
3. `docker export` produces a root filesystem tarball (`.tar.zst`).
4. The builder uploads the tarball to the control plane over the ingest channel. The control plane uploads it to Proxmox template storage through the API.
5. The control plane creates the template LXC with the following settings, then converts it to a template:
   - `unprivileged=1`, `features: nesting=1,keyctl=1`;
   - `ostype=ubuntu`;
   - `nameserver` set explicitly;
   - NIC on the job VNet with `firewall=1`;
   - security group attached;
   - `ghrm-template` tag.
6. The builder is destroyed. Build logs stream live to the UI like any other log stream.

### 8.4 Verification

A clone of the new template boots with the agent in **self-test mode**:

- Docker works: `hello-world`, a buildx build with the `docker-container` driver and a compose stack with a bind mount.
- DNS resolves, outbound HTTPS works, and LAN and hypervisor addresses are unreachable.
- The runner binary starts, and the agent reaches the ingest.
- **Fidelity report.** The official `generate-software-report.sh` shipped with the slim recipe is run, and its output is compared with the report GitHub publishes for the same release. Expected differences are only the items added by the ghrm layer. Any other difference is shown in the UI.

Only a template that passes verification becomes `active`. A canary workflow (Section 13) can optionally be run against it before activation.

### 8.5 Triggers, rollout and rollback

A daily check triggers a rebuild in any of these cases:

1. a new `ubuntu-slim/*` release in `actions/runner-images`;
2. a new `actions/runner` release. Outdated runners self-update at startup, which delays jobs, and very old versions are rejected by GitHub;
3. a new ghrm layer version, shipped with a new ghrm release.

New environments use the `active` template. Environments that are already running keep theirs. The previous version is retained for **one-click rollback** (keep N=2). Older versions are deleted once no environment references them. Operators can **pin** a version, which disables automatic rollout, **rebuild** or **roll back** from the UI. A failed build never changes the active template.

## 9. Scheduling and capacity

For each scale set:

```
desired = min(scale_set.max_concurrent, assigned_jobs + scale_set.warm_pool)
```

Creation is gated by global limits, which are evaluated at creation time:

| Limit | Rule |
|---|---|
| Environments | Global maximum of concurrent environments, plus the per-scale-set maximum |
| Memory | The sum of the memory limits of live environments must stay within a configured budget, **and** the host's available memory must be at least the environment's limit plus a safety margin (default 4 GiB). This protects other guests on a shared homelab host. |
| Disk | No new environments while thin pool data or metadata usage is above a threshold (default 85%) |
| CPU | Not limited; overcommit is allowed |

- When capacity is short, waiting jobs are served first-in, first-out across scale sets. The UI shows why each job waits, for example "waiting for memory" or "scale set limit reached".
- The warm pool defaults to 0. A warm pool trades idle memory for about 10 s less wait.

### 9.1 Reaper

The reaper runs every 30 s and at startup. It is idempotent.

- A runtime environment with the `ghrm-env` tag that is unknown to the database is destroyed.
- A database environment whose runtime guest no longer exists is marked `failed` or `destroyed`.
- A GitHub runner named `ghrm-*` that has no live environment is deregistered.
- The state timeouts in Section 5 are enforced.

After a control-plane restart, state is rebuilt from the three sources before scheduling resumes. Environments are neither leaked nor duplicated.

## 10. Networking and security

### 10.1 Job network

- A dedicated, IPv4-only network for job environments: a Proxmox SDN Simple zone with SNAT and dnsmasq DHCP, for example `10.50.0.0/24`.
- Security group `ghrm-job`, attached through the template:
  1. `OUT ACCEPT` UDP 67 (DHCP);
  2. `OUT ACCEPT` TCP to `<ingest address>:<ingest port>` (the only internal destination allowed);
  3. `IN DROP` everything;
  4. `OUT DROP` 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 and 169.254.0.0/16;
  5. everything else (the internet) is allowed.
- DNS servers are set explicitly on the template, using public resolvers by default.

### 10.2 Hypervisor credentials

- A dedicated Proxmox user (`ghrm@pve`) with an API token and a custom role. The role is scoped to a resource pool (`ghrm`), the template and rootfs storages and the job SDN zone. It grants clone, configure, power, destroy, audit and template upload. It never uses `root`.
- Because clones inherit the firewall from the template, the runtime does not need firewall-editing privileges in normal operation. Template creation does need them.
- **Open item:** whether setting the LXC `env` option needs privileges beyond the role above. If it does, the agent uses the fallback bootstrap (Section 14).

### 10.3 Firewall application window

A new guest must not start before `pve-firewall` has applied its rules. The runtime confirms that the rules are applied before `Start`. The exact mechanism is an implementation task (Section 14). Until it exists, a fixed delay longer than one compile cycle (≥ 12 s) is used.

### 10.4 GitHub credentials

- Fine-grained PATs with **Administration: read and write** on the target repositories (or **Self-hosted runners: read and write** on organizations). A PAT belongs to a single resource owner, so one credential is configured per owner.
- Secrets are encrypted at rest with a key stored in a separate `0600` file outside the database.
- Authentication sits behind an interface so that a GitHub App can be added later.

### 10.5 Agent channel

- TLS with a certificate generated by the control plane. The agent receives the certificate's fingerprint in its bootstrap and pins it.
- Each environment gets a random token. The database stores only its hash. The token is valid only while the environment is live and only for that environment's data.
- The JIT config is single-use and visible inside the job (as it is on any runner), which is acceptable.

### 10.6 Web UI access

- Authentication is required. The first-run flow creates an admin account. Passwords are hashed with argon2id, and sessions use secure, HttpOnly cookies. CSRF protection applies to state-changing requests.
- Recommended exposure: LAN only, or behind an identity-aware proxy (for example Cloudflare Access). OIDC is a future option.
- Every administrative action is recorded as an event (audit trail).

## 11. Web UI

The UI is built with React, Vite, Tailwind CSS v4 and **Kumo** (`@cloudflare/kumo`), and follows the interaction patterns of the Cloudflare dashboard.

### 11.1 Shell

- `sidebar` navigation, `breadcrumbs`, and a `page-header` block with title, description and a primary action.
- Resource lists use the `resource-list` block with `table`, `toolbar` (search and filters) and `pagination`. Detail pages use `tabs`.
- A `command-palette` (⌘K) jumps to any job, environment, commit SHA or scale set.
- A **Live** indicator shows the SSE connection state.
- Light and dark themes come from Kumo tokens.
- Destructive actions use the `delete-resource` block, which asks the operator to type the name to confirm.

### 11.2 Pages

1. **Overview**
   - KPIs: running jobs, queued jobs, jobs in the last 24 h, success rate, and median time from queued to started.
   - Capacity meters (`meter`) for memory, environments and disk.
   - A jobs-per-hour `chart`.
   - A live "Now" list of active environments.
   - Alert `banner`s: template build failed, GitHub credential invalid, hypervisor unreachable, reaper actions.
2. **Jobs**
   - A live table with filters for scale set, status, repository and time range (`date-range-picker`).
   - The job detail page has these tabs:
     - **Timeline:** every lifecycle stage with timestamps and durations; a failed stage is highlighted with its reason.
     - **Steps:** the GitHub step list; selecting a step opens its live log.
     - **Logs:** a stream selector and a live tail with pause and resume, search, level filter, wrap, copy and download.
     - **Resources:** CPU and memory charts.
     - **Environment:** runtime details and a link to the hypervisor console.
3. **Environments**
   - The same detail view, centred on the LXC. This view includes environments that failed before receiving a job.
   - Actions: destroy, keep for debugging, and open the console.
4. **Scale sets**
   - A list with the listener status (connected, last message, assigned jobs).
   - Configuration: labels, resources, limits, warm pool, keep-on-failure, and pause/resume.
   - A copyable `runs-on` snippet (`clipboard-text`).
5. **Templates**
   - Versions with the slim release, the runner version, the state and the size.
   - Build detail with a live build log and the fidelity report.
   - Actions: rebuild, pin and roll back.
6. **Live logs**
   - A global, filterable, pausable stream of all events, similar to Cloudflare's Workers Logs live view.
7. **Settings**
   - Hypervisor connection and GitHub credentials, using `sensitive-input`, each with a "Test connection" action.
   - Capacity limits, retention, and account.

### 11.3 Components outside Kumo

- **Log viewer.** A virtualized list built with TanStack Virtual and styled like Kumo's `code`. It renders ANSI colours and keeps tens of thousands of lines smooth.
- **Lifecycle timeline.** Kumo's `flow` component is evaluated first. If it does not fit, a small custom component is built on Kumo tokens.

### 11.4 Usability details

- Relative timestamps, with the absolute time in a tooltip.
- Skeleton loading states, `empty` states with guidance, and `toast` feedback for actions.
- Live updates never reorder rows under the cursor.
- The layout is responsive, so the dashboard is usable on a phone.

## 12. Deployment and operations

### 12.1 Proxmox (primary)

An idempotent `install.sh`, run once in the Proxmox host shell, performs these steps:

1. It creates the **control-plane LXC**: Debian 13, 1 vCPU, 1–2 GiB RAM, a small root disk plus a data volume. The LXC has two NICs: one on the LAN (UI and hypervisor API) and one on the job network with a fixed IP reserved outside the DHCP range (ingest only).
2. It creates the Proxmox user, role, resource pool and API token.
3. It ensures that the SDN zone, VNet, subnet and security group exist, including the ingest exception rule placed before the LAN drop rules.
4. It installs `ghrm` as a systemd service and prints the URL for first-run setup (admin account, GitHub credentials, scale sets, first template build).

To upgrade, re-run the installer or use `ghrm self-update`. Database migrations run automatically at startup.

### 12.2 Docker (local)

`docker compose up` runs the control plane in a container against a remote Proxmox host. This is intended for development, or for operators who prefer to run the control plane outside Proxmox. The ingest address is then the Docker host's address, and the security group exception is generated for that address (configurable).

### 12.3 Operations

- `/healthz`, `/readyz` and Prometheus `/metrics` (queue depth, environments by state, stage durations, failures by stage, build results).
- Structured JSON logs (`log/slog`).
- A daily SQLite backup with `VACUUM INTO` to the data volume. Operators should also back up the control-plane LXC with Proxmox backups.

## 13. Implementation choices and testing

### 13.1 Stack

- **Backend:** Go, standard library `net/http` routing, `huma` for OpenAPI-first handlers, `sqlc` and `goose` for typed queries and migrations, and `modernc.org/sqlite` for CGO-free, static binaries. Additional libraries: `github.com/actions/scaleset` and a Proxmox API client (or a thin internal client).
- **Agent:** Go, static, with no runtime dependencies inside the job image.
- **Web:** React, Vite, TypeScript, Tailwind CSS v4, `@cloudflare/kumo`, TanStack Router, TanStack Query and TanStack Virtual. The API client is generated from the OpenAPI document with `openapi-typescript` and `openapi-fetch`, so the UI is type-checked against the backend. The built UI is embedded in the `ghrm` binary.
- **Why Go rather than one TypeScript stack:** the core depends on the official scale set client, which is Go. Go also gives a small, dependency-free agent that cannot interfere with toolchains installed by jobs, and a single-binary deployment. UI type safety is preserved through the generated client.

### 13.2 Repository layout

```
gh-runners-manager/
├─ cmd/ghrm/              control plane (serve, template, migrate, admin subcommands)
├─ cmd/ghrm-agent/        in-environment agent
├─ internal/
│  ├─ scaleset/           listener built on actions/scaleset
│  ├─ scheduler/          pure capacity and desired-state logic
│  ├─ runtime/            interface + runtime/proxmoxlxc/
│  ├─ template/           build, verification, rollout
│  ├─ store/              SQLite schema, migrations, queries
│  ├─ events/             event bus and SSE
│  ├─ ingest/             agent ingest
│  ├─ api/                HTTP API and OpenAPI
│  ├─ auth/  secrets/     UI authentication, encrypted secrets
├─ web/                   React + Kumo application
├─ template/layer/        ghrm layer Dockerfile and files
├─ deploy/proxmox/        install.sh
├─ deploy/docker/         compose.yml
└─ docs/
```

### 13.3 Testing

| Level | Scope | Approach |
|---|---|---|
| Unit | Scheduler, state machine, reaper decisions, timeouts | Pure functions over simulated state |
| Integration | The whole control plane | Fake `Runtime` and fake GitHub scale set server behind interfaces |
| Runtime contract | `proxmox-lxc` | Opt-in tests against a real Proxmox host inside the reserved VMID range, with guaranteed cleanup |
| Canary end-to-end | The real system with real jobs | A test repository whose workflow covers `services:`, `container:`, compose with bind mounts, buildx, `sudo apt`, system Node.js and locale-sensitive commands. Also used to validate new templates. |
| Web | Components and flows | Vitest with Testing Library; Playwright against a backend that uses the fake runtime |

## 14. Risks and open items

| # | Item | Mitigation / next step |
|---|---|---|
| 1 | The `actions/scaleset` client is in public preview, and its API may change. | Pin versions and wrap the client behind an internal interface. Confirm repository-level scale sets for personal accounts early. |
| 2 | ~~The LXC `env` option may need privileges beyond the scoped role.~~ | **Resolved in M1:** `ghrm smoke` with the scoped `GhrmRuntime` role on Proxmox VE 9.2 sets `env` successfully. Minimum version: Proxmox VE 9.1. |
| 3 | No API confirms when `pve-firewall` has applied rules for a new guest. | Investigate a reliable signal. Until then, use a fixed delay longer than one compile cycle. |
| 4 | Converting the `ubuntu-slim` image into an LXC root filesystem: the image has no init, and Proxmox's `ostype=ubuntu` network setup must work with it. | The ghrm layer installs systemd. Validate in the template milestone, including the software-report comparison. |
| 5 | Every job starts with a cold Docker cache, which makes buildx-heavy jobs slower than on hosted runners. | Future: a pull-through registry mirror on the job network, and BuildKit cache exports (`type=gha` already works). |
| 6 | Docker Engine inside an unprivileged LXC with `nesting=1` is a supported but less common setup. | Covered by the canary suite and by template verification on every build. |
| 8 | The scoped role cannot read thin pool metadata usage (`/disks/lvmthin` needs `Sys.Audit` on `/`). | Found in M1. Disk usage falls back to the storage status (data only); operators can grant `Sys.Audit` on `/` to also check metadata. |
| 7 | Some workflows may rely on tools that hosted `ubuntu-24.04` has and `ubuntu-slim` lacks. | Documented as a known difference. The fidelity report makes the tool set visible. A "full" profile can be added later if needed. |

## 15. Future work (out of scope)

- Additional runtimes: Docker host, cloud VMs, VM-per-job on Proxmox for kernel-level isolation.
- GitHub App authentication and organization-level scale sets for multiple owners.
- A pull-through registry mirror and cache services on the job network.
- Notifications (email, webhook) and OIDC sign-in.
- Multi-node Proxmox scheduling.
