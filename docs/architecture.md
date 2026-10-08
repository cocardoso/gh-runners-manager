# Architecture

This document holds the architecture diagrams of gh-runners-manager (`ghrm`). It is updated in the same change as any code that alters components, flows, networking or states. The [design document](design.md) explains the reasoning behind the design.

## Components

| Component | Package / location |
|---|---|
| Configuration | `internal/config` |
| Environment state machine | `internal/environment` |
| Capacity scheduler (pure logic) | `internal/scheduler` |
| Runtime interface and in-memory fake | `internal/runtime`, `internal/runtime/runtimetest` |
| Proxmox API client and fake server | `internal/proxmox`, `internal/proxmox/proxmoxtest` |
| `proxmox-lxc` runtime | `internal/runtime/proxmoxlxc` |
| Store (SQLite) | `internal/store` |
| Event bus and recorder | `internal/events` |
| Log store (files + follow) | `internal/logs` |
| Scale set adapter (`actions/scaleset`) | `internal/github` |
| Controller and reaper | `internal/controller` |
| Ingest (TLS, per-environment tokens) | `internal/ingest` |
| Agent | `cmd/ghrm-agent`, `internal/agent` |
| REST API and SSE | `internal/api` |
| `ghrm version`, `smoke`, `serve`, `openapi`, `demo` | `cmd/ghrm` |
| Simulated fleet for UI work and browser tests | `internal/demo` |
| Web UI (React, Kumo), embedded in the binary | `web/` |
| UI languages (typed dictionaries, one file per area with every language) | `web/src/i18n` |
| Template builder (`ubuntu-slim`): build, verify, activate, retain, release checks | `internal/template`, `template/layer` |
| Agent build and self-test modes | `internal/agent` (`build.go`, `selftest.go`) |
| Sign-in: admin account (argon2id), sessions, CSRF, audit | `internal/auth`, `internal/api/auth.go` |
| Secrets sealed at rest (AES-256-GCM, separate key file), `ghrm secret` | `internal/secrets` |
| Editable credentials and scale sets, listener supervisor | `internal/settings`, `cmd/ghrm/supervisor.go` |
| Repository picker and token check: what a credential can register runners for (`GET /api/v1/credentials/{name}/targets`, `POST /api/v1/credentials/check`) | `internal/github/targets.go`, `internal/api/credential_targets.go` |
| Prometheus metrics, daily backups | `internal/metrics`, `internal/backup` |
| Repositories view: scale sets and job activity per GitHub repository or organization (`GET /api/v1/repositories`) | `internal/api/repositories.go`, `internal/store/activity.go` |
| History retention: daily or manual cleanup of finished environments, jobs, events, logs and failed template records | `internal/retention`, `internal/store/history.go` |
| Registry cache: proxies, eviction, disk exporter, monitor, mirror settings in templates | `internal/cachemon`, `internal/agent` (`cacheprune.go`, `cacheexporter.go`), `template/layer/mirrors.sh` |
| Installer, container image, releases | `deploy/proxmox/install.sh`, `Dockerfile`, `deploy/docker`, `.github/workflows/release.yml` |

## 1. System overview

```mermaid
flowchart LR
    subgraph github["GitHub"]
        ss["Runner Scale Set API"]
        rest["REST API"]
    end

    subgraph cp["Control plane: ghrm (single Go binary)"]
        settings["Settings: file + UI credentials and scale sets"]
        vault["Vault: secrets sealed with a separate key file"]
        auth["Sign-in: account, sessions, CSRF"]
        metrics["/metrics, daily backups, history cleanup"]
        listener["Scale set listeners (supervised)"]
        scheduler["Scheduler"]
        runtime["Runtime: proxmox-lxc"]
        reaper["Reaper"]
        templates["Template service"]
        store["Store: SQLite + log files"]
        ingest["Ingest"]
        api["REST API + SSE"]
        ui["Web UI (Kumo)"]
    end

    subgraph pve["Proxmox VE host"]
        pveapi["Proxmox API"]
        tmpl["Active template LXC"]
        tmplstore["Template storage (vztmpl archives)"]
        subgraph jobnet["Isolated job network"]
            env1["Job LXC + ghrm-agent"]
            env2["Job LXC + ghrm-agent"]
            builder["Builder / verify LXC + ghrm-agent (build, self-test mode)"]
            cache["Registry cache LXC: one proxy per registry<br/>(Docker Hub, GHCR, MCR, Quay)"]
        end
    end

    operator(["Operator browser"])

    listener -- "long-poll (outbound)" --> ss
    listener --> scheduler
    scheduler --> runtime
    runtime -- "clone, configure, start, destroy" --> pveapi
    pveapi -. "linked clone" .-> tmpl
    tmpl -. "clone of" .-> env1
    tmpl -. "clone of" .-> env2
    env1 -- "events + logs (HTTPS)" --> ingest
    env2 -- "events + logs (HTTPS)" --> ingest
    env1 -- "runner protocol (outbound)" --> github
    env1 -- "image pulls (mirrors)" --> cache
    cache -- "first pull only" --> registries(["Container registries"])
    templates -. "health, hits, disk" .-> cache
    reaper --> runtime
    reaper --> rest
    templates -- "release checks" --> rest
    templates -- "upload archive, create, convert" --> pveapi
    pveapi -.-> tmplstore
    tmpl -. "clone of" .-> builder
    builder -- "build log, root filesystem, self-test report" --> ingest
    ingest --> templates
    ingest --> store
    scheduler --> store
    api --> store
    ui --> api
    operator --> ui
    api --> auth
    api --> settings
    settings --> vault
    settings -- "start, restart, stop" --> listener
    metrics --> store

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class runtime,scheduler,listener,reaper,store,ingest,api,ui,templates,settings,vault,auth,metrics,cache done
```

## 1a. Web UI data flow

The UI is a single-page app embedded in `ghrm` (`web/embed.go`) and served for every non-API path. Server state is fetched over REST through a client generated from the OpenAPI document. One shared event stream keeps every page live by invalidating the queries an event affects; each open log view has its own stream.

```mermaid
flowchart LR
    subgraph browser["Operator browser"]
        pages["Pages: overview<br/>inventory: repositories, scale sets, templates<br/>activity: jobs, environments, events<br/>settings"]
        query["TanStack Query cache"]
        live["Shared EventStream<br/>(backoff, resume after seq, stale detection)"]
        viewer["Log viewer (virtualized, ANSI)<br/>LogBuffer cap 50k lines"]
        follower["LogFollower per open log"]
    end
    subgraph ghrm["ghrm"]
        static["Embedded UI (SPA fallback)"]
        rest["REST: /api/v1/* (huma, OpenAPI)"]
        evsse["SSE: /api/v1/events/stream<br/>after=latest | after=seq, named ping every 15 s"]
        logsse["SSE: …/logs/{stream}?follow=true&offset="]
        tail["REST: …/logs/{stream}?tail=true&before="]
    end
    pages --> query
    query -- "GET (generated client)" --> rest
    live -- "one EventSource" --> evsse
    live -- "invalidate by event kind (batched 250 ms)" --> query
    viewer --> follower
    follower -- "last page first, load earlier" --> tail
    follower -- "follow from last offset; closed while paused" --> logsse
    browser -- "first load" --> static

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class pages,query,live,viewer,follower,static,rest,evsse,logsse,tail done
```

- A fresh page starts the event stream at `after=latest` and reconnects with `after=<last seq>`, so a control-plane restart or a network blip replays what was missed. A connection that stays silent past two heartbeats is treated as stale and replaced; the Live indicator shows `Reconnecting` meanwhile.
- Every API call needs a signed-in session (or the admin bearer token, for scripts). The shell checks the session first and shows the first-run form or the sign-in form in place of the page; writes send the session's CSRF token, and a 401 shows the sign-in form again.
- Mutating actions (destroy an environment, build, activate or pin a template, edit credentials and scale sets) are recorded as `audit.*` events with the actor.

## 1b. Sign-in and API access

```mermaid
sequenceDiagram
    autonumber
    participant B as Browser
    participant A as API middleware
    participant S as auth.Service
    participant D as SQLite

    B->>A: GET /api/v1/auth/session
    A->>S: needs setup? / session from cookie
    S->>D: users, sessions (cookie stored as its SHA-256)
    A-->>B: setup | signed_out | signed_in + CSRF token
    alt first run
        B->>A: POST /auth/setup (setup token from data_dir/setup-token)
        A->>S: create the admin (argon2id), remove the setup token
    end
    B->>A: POST /auth/login
    A->>S: verify (per-address lockout after 5 failures)
    A-->>B: Set-Cookie ghrm_session (HttpOnly, SameSite=Strict, Secure behind HTTPS)
    B->>A: POST /api/v1/... + X-CSRF-Token
    A->>S: session (slides its expiry), CSRF check
    A->>A: handler; audit.* event with the actor
    Note over A: scripts send Authorization: Bearer <admin token> instead (no CSRF)
```

## 1c. Editable settings

Credentials and scale sets come from two places: `ghrm.yaml` (read-only in the UI) and the UI (stored in SQLite; tokens sealed in the vault under `github/<name>`). A change applies without a restart. Template profiles are edited in the UI only (table `template_profiles`); a profile a scale set uses cannot be deleted, and the default one always exists.

```mermaid
flowchart LR
    file["ghrm.yaml"] --> reg["settings.Registry"]
    ui["UI: Settings, Scale sets"] -- "PUT / DELETE (audited)" --> reg
    reg -- "tokens" --> vault["Vault (AES-256-GCM, key in secret.key 0600)"]
    reg -- "rows" --> db[("SQLite")]
    reg -- "change" --> ctl["Controller: add, update, drain removed"]
    reg -- "change" --> sup["Supervisor: start, restart, stop listeners"]
    reg -- "current token" --> gh["GitHub client (rebuilt when URL or token change)"]
    ui -- "GET credentials/{name}/targets, POST credentials/check" --> tgt["Targets (5-minute cache per credential and token)"]
    reg -- "token" --> tgt
    tgt -- "GET /user/repos, /user/memberships/orgs" --> ghrest["GitHub REST API"]

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class file,reg,ui,vault,db,ctl,sup,gh,tgt,ghrest done
```

The scale set form picks the repository or organization from what the credential can register runners for: the repositories it administers (`/user/repos`, `permissions.admin`, up to 1,000, then the list says it is cut) and their organizations (only those it administers when the token may read its memberships). The list is cached for 5 minutes per credential and token hash (`?refresh=true` asks again unless the list is under 10 seconds old; two requests at once share one listing); a GitHub error answers 502 with GitHub's message, and the form falls back to typing the URL. `POST /api/v1/credentials/check` tests a token before it is saved: whose it is and how many repositories and organizations it reaches. A new scale set is saved with `If-None-Match: *`, so it never replaces one of the same name (412).

A removed scale set drains: its running environments finish, it gets no new ones, and its listener keeps running (with the last GitHub client and token, so job messages and runner removal still work) until the last environment is gone; then it disappears. It stays registered on GitHub (without runners) until deleted there.

## 2. Network and isolation

```mermaid
flowchart LR
    subgraph jobnet["Job network: SDN Simple zone, SNAT, DHCP, IPv4 only"]
        job["Job LXC (unprivileged)"]
        gw["SDN gateway (on the Proxmox host)"]
        cpjob["ghrm: job-network interface (ingest only)"]
        cache["Registry cache (.3): mirror ports only"]
    end

    internet(["Internet: GitHub, registries, package mirrors"])

    subgraph lan["Operator LAN"]
        router["Router / gateway"]
        hostapi["Proxmox host: API :8006, SSH"]
        others["Other guests and devices"]
        cplan["ghrm: LAN interface (UI, Proxmox API client)"]
    end

    job -->|"DHCP"| gw
    job -->|"ingest port only"| cpjob
    job -->|"mirror ports 5000-5003"| cache
    cache ==>|"first pull, via SNAT"| internet
    cpjob -. "metrics and disk ports (control plane only)" .-> cache
    job ==>|"everything else, via SNAT"| internet
    job -. "blocked by the security group" .-x lan
    cplan --> hostapi
```

Security group applied to every job LXC (inherited from the template):

| Order | Direction | Rule |
|---|---|---|
| 1 | out | ACCEPT UDP 67 (DHCP) |
| 2 | out | ACCEPT TCP to the ingest address and port |
| 2b | out | ACCEPT TCP to the registry cache's mirror ports (5000–5003), when a cache is configured |
| 3 | in | DROP everything |
| 4 | out | DROP 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 |
| — | out | everything else (internet) is allowed |

Nothing can open a connection into a job LXC (rule 3). The ingest (rule 2) and the registry cache's mirror ports (rule 2b) are the only internal destinations a job can reach; the cache's metrics and disk ports stay closed to jobs (the template self-test checks it).

### 2a. Registry cache

Job templates point the Docker daemon (`registry-mirrors`, Docker Hub), containerd (`/etc/docker/certs.d/<registry>/hosts.toml`) and BuildKit (`~/.docker/buildx/buildkitd.default.toml` of the runner user, read when `setup-buildx-action` creates a builder) at the cache, so `services:`, `container:`, `docker pull`, `docker build` and buildx pull through it with unchanged workflows. If the cache does not answer, pulls go straight to the registry. The cache container runs one CNCF Distribution proxy per registry; `ghrm-agent cache-prune` (every 15 minutes) evicts the least recently used repositories above 85 % of its disk budget, and `ghrm-agent cache-exporter` reports the disk use. The control plane polls it every 30 s for the overview alert, the Settings card and the `ghrm_cache_*` metrics. Changing the cache settings changes the layer version, so templates are rebuilt.

## 3. Job lifecycle

```mermaid
sequenceDiagram
    autonumber
    participant GH as GitHub (scale set)
    participant L as Listener
    participant C as Controller
    participant R as Runtime (proxmox-lxc)
    participant P as Proxmox API
    participant E as Job LXC (ghrm-agent)
    participant I as Ingest
    participant UI as API / SSE clients

    GH-->>L: JobAvailable (the only message with the queue time)
    L->>C: job assigned, queued at
    GH-->>L: statistics: assigned jobs = N
    L->>C: desired count
    C->>C: scheduler: capacity check (memory, disk, limits)
    C->>GH: generate JIT runner config
    C->>R: Create(spec: JIT config, ingest URL, token, fingerprint in env)
    R->>P: nextid probe, linked clone, tag, configure, wait for firewall
    C->>R: Start
    R->>P: start
    E->>I: hello, runner_started (TLS pinned, bearer token)
    E->>GH: runner online, takes one job
    E->>I: runner_online, job_started, live job log, metrics
    I->>C: job_started: the queued job with that name runs now
    GH-->>L: JobStarted (often tens of seconds late) / JobCompleted
    E->>I: job_finished, runner_exited, shutdown
    E->>E: power off
    C->>R: Destroy (stop if needed, delete)
    C->>GH: remove runner (if still registered)
    I-->>UI: every step is an event and a log line, streamed live
```

GitHub's JobStarted message lags the runner by 10–30 seconds, sometimes past the job's end, so the agent's `job_started` marks the job running: it carries the job's name, which is matched to the scale set's one job with that name queued in the last 24 hours (two such jobs, as in a matrix or an organization's repositories, wait for GitHub's message). Both sides claim the job with one conditional update, so only one records the start; GitHub's message then completes the record and never reopens a finished job. A job still queued after 24 hours, which GitHub has canceled, is closed as canceled.

A guest starts without waiting for `pve-firewall` when its template's agent gates the runner itself. The control plane serves a probe on the ingest port + 1, which the job security group drops, and passes it as an IP address in `GHRM_FIREWALL_PROBE`. Every 500 ms the agent opens three connections to it at once; once the probe has answered (connected or refused), a round where all three time out means the group applies, and the runner starts. A host unreachable, a slow resolver or a single lost SYN is no proof. A probe that never answered proves nothing, so the agent then keeps the `firewall_settle` delay from its own start (`GHRM_FIREWALL_SETTLE`); a probe still answering after 60 s fails the environment at stage `firewall` (a guest stopped meanwhile reports nothing). A template gates only when its verification proves it: the agent reports the `firewall-gate` feature and the self-test finds the probe dropped (a warning otherwise, with a `template.firewall_delay` event). Build and verify environments, the bootstrap template, templates not proved, a probe that could not bind, and an ingest advertised on another port than it listens on keep the delay before the start. The group must DROP the probe: Proxmox's REJECT answers with a reset.

Warm runners: a scale set's `warm_runners` are created before any job arrives, within `max_concurrent` (desired = min(assigned + warm, max_concurrent), or assigned alone above the limit). The scheduler serves assigned jobs first and gives warm runners only the capacity left, and missing warm runners never count as waiting jobs. The reaper keeps an idle runner past the idle timeout while its scale set has no more idle runners than `warm_runners`, oldest released first; it replaces a warm runner after an hour (a newer template), but not while jobs are queued, and a removed scale set keeps none.

Proxmox reads are sent up to three times when the answer is a passing server error (the LXC list answers 500 now and then while a guest starts or stops: `failed to read from command socket`; 502–504, 595 and 596 during a pveproxy reload) or the connection drops. A missing guest, a timeout and an answer that does not decode are final. The overview shares one capacity call for 5 seconds and keeps the last good answer for a minute, so a single failed call is not shown as an unreachable runtime; callers wait for the shared call only as long as their own request lives. A token that may not read `/disks/lvmthin` is asked again every 10 minutes, so a permission granted later takes effect without a restart.

Agent → ingest frames are NDJSON batches every 250 ms. Each log stream has its own sequence numbers, so a retried batch is stored once; an agent numbers from its start time in microseconds, so an agent that restarts (a builder updating itself) continues above its predecessor. Events share the `agent` stream's sequence and reach the controller exactly once.

## 3a. Where each log stream comes from

```mermaid
flowchart LR
    subgraph env["Job LXC"]
        stdout["run.sh stdout/stderr"]
        diag["_diag/Runner_*.log, Worker_*.log"]
        pages["_diag/pages/&lt;plan&gt;_&lt;job record&gt;_&lt;n&gt;.log (whole-job log)"]
        cg["cgroup v2: cpu.stat, memory.current"]
        agentlog["agent messages and events"]
    end
    subgraph cp["Control plane"]
        ctl["controller decisions"]
        rt["runtime operations"]
    end
    stdout --> runner["stream: runner"]
    diag --> runner
    pages --> job["stream: job"]
    cg --> metrics["stream: metrics"]
    agentlog --> agent["stream: agent"]
    ctl --> controlplane["stream: control-plane"]
    rt --> runtimes["stream: runtime"]
```

The runner writes one log per step and one for the whole job. The agent learns the job's record ID from the diagnostic log (`Job request … job <id> received`) and streams only the whole-job log, in numeric page order.

## 4. Environment states

Implemented in `internal/environment`; transitions are compare-and-set in the store. The reaper enforces every timeout. It concludes that a guest powered off silently only after 60 s in the state and a live status check, because the Proxmox LXC listing is cached and lags behind a start.

```mermaid
stateDiagram-v2
    [*] --> pending
    pending --> provisioning
    provisioning --> booting: clone ready, firewall applied (timeout 2m)
    booting --> connected: agent hello (timeout 2m)
    connected --> idle: runner online (timeout 2m)
    idle --> running: job assigned (timeout 10m without a job)
    idle --> completing
    running --> completing: job finished (timeout 6h)
    completing --> destroying: logs flushed, powered off (timeout 5m)
    pending --> failed
    provisioning --> failed
    booting --> failed
    connected --> failed
    idle --> failed
    running --> failed
    completing --> failed
    failed --> destroying: after keep-on-failure window
    pending --> destroying
    provisioning --> destroying
    booting --> destroying
    connected --> destroying
    idle --> destroying
    running --> destroying
    destroying --> destroyed
    destroyed --> [*]
```

## 5. `proxmox-lxc` runtime: create and destroy

How the runtime avoids leaking guests and never touches guests it does not own.

```mermaid
flowchart TD
    start(["Create(spec)"]) --> validate{"spec valid?"}
    validate -- no --> reject(["ErrInvalidSpec, no API calls"])
    validate -- yes --> idlock["lock environment ID"]
    idlock --> existing{"guest tagged ghrmid-ID exists?"}
    existing -- yes --> same(["return existing ref"])
    existing -- no --> alloc["allocation lock: lowest free VMID in range<br/>(skip listed guests, probe the rest with nextid)"]
    alloc --> clone["linked clone of the template"]
    clone -- "task started but failed or cancelled" --> waitclean["wait for the clone task, then destroy"]
    clone -- ok --> unlockalloc["release allocation lock"]
    unlockalloc --> tag["tag: ghrm-env, ghrmid-ID"]
    tag --> configure["configure: cores, memory, swap, env"]
    configure --> gated{"agent gates the firewall?<br/>(proved by the template's verification)"}
    gated -- yes --> ref(["return ref VMID/ID"])
    gated -- no --> settle["wait for pve-firewall (firewall_settle)"]
    settle --> ref
    tag -- error --> cleanup["destroy clone; report cleanup failure with the VMID"]
    configure -- error --> cleanup
    settle -- cancelled --> cleanup

    dstart(["Destroy(ref VMID/ID)"]) --> range{"VMID in range?"}
    range -- no --> notowned(["ErrNotOwned"])
    range -- yes --> look["find guest in the LXC list<br/>(only guests in the token's pool are visible)"]
    look --> mine{"present and tagged ghrmid-ID?"}
    mine -- no --> gone(["nil: already gone or VMID reused"])
    mine -- yes --> running{"running?"}
    running -- yes --> stop["stop"]
    running -- no --> settle2["wait a few seconds: a guest that powered itself off<br/>may still be unmounting its disk"]
    settle2 --> del["delete (purge)"]
    stop --> del
    stop -- "error: re-read and retry" --> look
    del -- "error: re-read and retry" --> look
    del --> done(["nil"])
    del -- "task ends with WARNINGS (e.g. disk in use)" --> warn["log + proxmox.task_warnings event<br/>(leftovers on the host need a look)"]
    warn --> done
```

## 6. Code map

```mermaid
flowchart LR
    ghrm["cmd/ghrm"] --> config["internal/config"]
    ghrm --> api["internal/api"]
    ghrm --> controller["internal/controller"]
    ghrm --> ingest["internal/ingest"]
    ghrm --> github["internal/github"]
    ghrm --> proxmoxlxc["internal/runtime/proxmoxlxc"]
    ghrm --> demo["internal/demo"]
    ghrm --> template["internal/template"]
    ghrm --> settingsp["internal/settings"]
    ghrm --> authp["internal/auth"]
    ghrm --> metricsp["internal/metrics"]
    ghrm --> backupp["internal/backup"]
    ghrm --> retentionp["internal/retention"]
    retentionp --> store
    retentionp --> logs
    settingsp --> secretsp["internal/secrets"]
    settingsp --> store
    secretsp --> store
    authp --> store
    api --> authp
    api --> settingsp
    metricsp --> controller
    github --> settingsp
    template --> controller
    template --> layer["template/layer"]
    ghrm --> webui["web (embedded UI)"]
    demo --> controller
    demo --> runtimetest
    controller --> scheduler["internal/scheduler"]
    controller --> environment["internal/environment"]
    controller --> runtime["internal/runtime"]
    controller --> store["internal/store"]
    controller --> events["internal/events"]
    controller --> logs["internal/logs"]
    api --> store
    api --> events
    api --> logs
    ingest --> logs
    ingest --> events
    github --> scaleset[("actions/scaleset")]
    proxmoxlxc --> runtime
    proxmoxlxc --> proxmox["internal/proxmox"]
    agentcmd["cmd/ghrm-agent"] --> agent["internal/agent"]
    agent --> ingestproto["internal/ingest (protocol)"]
    runtimetest["internal/runtime/runtimetest"] --> runtime
    proxmoxtest["internal/proxmox/proxmoxtest"]

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef testonly fill:#fff3bf,stroke:#e67700,color:#000
    classDef ext fill:#e7f5ff,stroke:#1971c2,color:#000
    class ghrm,config,api,controller,ingest,github,proxmoxlxc,scheduler,environment,runtime,store,events,logs,proxmox,agentcmd,agent,ingestproto,demo,webui,template,layer,settingsp,authp,metricsp,backupp,retentionp,secretsp done
    class runtimetest,proxmoxtest testonly
    class scaleset ext
```

Yellow packages are test doubles; `internal/demo` uses the fake runtime to serve a simulated fleet (`ghrm demo`). `cmd/ghrm-agent` shares only the wire protocol with the control plane.

## 7. Template pipeline

Templates come in **profiles**. A profile says what of GitHub's recipe to leave out (its optional install scripts: the cloud CLIs, nvm, Node.js and Python on the PATH, yq, zstd, ...), what to preinstall in the hosted tool cache (Node.js, Python and Go versions, from the actions/*-versions manifests, so `actions/setup-*` finds them instead of downloading them in every job), extra Ubuntu packages and a build script. The `default` profile always exists: GitHub's recipe unchanged, plus Node.js 22 and 24 in the tool cache. Each profile has its own versions, one active at a time; a scale set picks its profile (`template_profile`) and clones that profile's active version, or the default profile's until it has one. The checker walks the profiles in order and builds one at a time: a profile is rebuilt when a release changes or its layer version does, which hashes the layer files, the agent, the cache settings and the profile. Leaving tools out shortens builds and saves disk; jobs do not start faster, as clones are linked.

A build clones the default profile's **active template** into a builder environment (it already has Docker, systemd and `ghrm-agent`), because the Proxmox API cannot run commands inside a fresh stock container. The very first template is the bootstrap template (`proxmox.template_vmid`), which the installer creates. Since the builder runs the active template's agent, which can be older than the control plane, it first replaces itself with the control plane's agent, so fixes to the build take effect in the next build.

The fidelity report compares the template's software report with the one GitHub publishes as an asset of the same `ubuntu-slim` release (the recipe's `ubuntu-slim-Report.json` is not refreshed for every release, so it is only a fallback). The recipe installs the latest releases at build time, so a build made after GitHub's shows newer versions: those differences are listed with their reason but do not hold the version back. A missing tool, an older version than GitHub's, a new major version of a language runtime, or an extra tool the layer does not install does; the tools a profile leaves out and the packages and cached versions it adds are explained by the profile.

```mermaid
flowchart LR
    rel["actions/runner-images release ubuntu-slim/*"] --> b1
    runner["actions/runner release + SHA-256"] --> b2
    layer["ghrm layer (template/layer, embedded in ghrm)"] --> b2
    subgraph builder["Builder LXC (clone of the active template, job network)"]
        b0["agent update: a builder whose ghrm-agent differs from the control plane's<br/>downloads it (GET /ingest/v1/build/agent), checks the SHA-256, re-executes"] --> b1
        b1["docker build: official ubuntu-slim Dockerfile<br/>(minus the install scripts the profile leaves out)"] --> b2["docker build: ghrm layer (systemd, Docker Engine, runner,<br/>profile.sh: apt packages, tool cache, script; agent)"]
        b2 --> b3["docker export, drop container markers, zstd, SHA-256"]
    end
    b3 -- "PUT /ingest/v1/build/rootfs (streamed, size-capped)" --> cp["control plane: verify SHA-256"]
    cp --> up["upload to template storage (Proxmox verifies the SHA-256)"]
    up --> create["create LXC: unprivileged, nesting (keyctl is reserved to root@pam), DNS, firewalled NIC, gh-runner group; convert to template"]
    create --> verify["verify LXC (clone): self-test + software report"]
    verify --> compare["compare with the report published with the release<br/>(asset internal.ubuntu-slim.json)"]
    compare -- "all checks pass, no unexpected differences, nothing pinned" --> active["active template"]
    compare -- "unexpected differences or pinned" --> ready["ready (manual activation)"]
    verify -- "a check fails, timeout" --> failed["failed: guests, template and archive removed"]
    b1 -- "builder stops, timeout, restart" --> failed
    b3 -- "bad SHA-256, too large" --> failed
    create -- "Proxmox error, restart" --> failed

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class rel,runner,layer,b0,b1,b2,b3,cp,up,create,verify,compare,active,ready,failed done
```

### 7a. Template version states

```mermaid
stateDiagram-v2
    [*] --> building: Build (manual or release check)
    building --> creating: archive received
    creating --> verifying: template created
    verifying --> ready: checks pass
    ready --> active: auto (no unexpected differences, nothing pinned) or manual
    active --> ready: another version activated (kept for roll-back)
    ready --> retired: not kept (see retention)
    retired --> deleted: no environment uses it
    building --> failed
    creating --> failed
    verifying --> failed: check failed, timeout, restart
    failed --> [*]
    deleted --> [*]
```

A failed build never changes the active template. Only one build runs at a time. The bootstrap template is never deleted.

Retention keeps the active version, the newest `keep - 1` versions that were active before (roll-back targets), the newest version that was never activated (awaiting review), and every pinned version. Other built versions are retired, and deleted once no environment uses them: neither a live environment recorded as cloned from the template nor a linked clone the hypervisor reports.

## 8. Deployment

`deploy/proxmox/install.sh` sets up a Proxmox VE host in one run; every step checks first, so it also resumes and upgrades (`--dry-run` shows what would change).

```mermaid
flowchart TD
    pre["Proxmox VE 9.1+ and root"] --> acc["pool ghrm, roles GhrmRuntime and GhrmTemplates,<br/>user ghrm@pve, API token, ACLs"]
    acc --> stor["template storage ghrm-tpl (dir, vztmpl)"]
    stor --> net["SDN zone + VNet + subnet (DHCP, SNAT); dnsmasq"]
    net --> fw["datacenter firewall on; security group:<br/>ingest, cache mirrors, DHCP, no inbound, no private ranges"]
    fw --> cache["registry cache LXC (.3): one proxy per registry,<br/>eviction timer, disk exporter (skip with --no-cache)"]
    cache --> cp["control-plane LXC (Debian 13): LAN + job-network NICs"]
    cp --> bin["ghrm + ghrm-agent from the release (SHA256SUMS),<br/>ghrm.yaml, admin token, token secret into the vault, service"]
    bin --> tpl["bootstrap template (Ubuntu 24.04, Docker, runner, ghrm-agent)"]
    tpl --> done(["prints the UI address and the setup token"])

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class pre,acc,stor,net,fw,cache,cp,bin,tpl,done done
```

After the first sign-in, the operator adds a GitHub credential and a scale set and builds the first real template (Templates > Build now); ghrm then rebuilds templates on new releases by itself. The same image runs with Docker Compose (`deploy/docker/compose.yaml`) against a remote Proxmox host. A `v*` tag publishes the binaries, `SHA256SUMS`, the installer and the container image.
