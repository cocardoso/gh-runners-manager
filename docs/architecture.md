# Architecture

This document holds the architecture diagrams of gh-runners-manager (`ghrm`). It is updated in the same change as any code that alters components, flows, networking or states. The [design document](superpowers/specs/2026-10-07-gh-runners-manager-design.md) explains the reasoning behind the design.

Legend used in the diagrams: **green** = implemented, **grey dashed** = planned (with its milestone).

## Implementation status

| Component | Package / location | Status |
|---|---|---|
| Configuration | `internal/config` | Implemented (M1, M2) |
| Environment state machine | `internal/environment` | Implemented (M1) |
| Capacity scheduler (pure logic) | `internal/scheduler` | Implemented (M1) |
| Runtime interface and in-memory fake | `internal/runtime`, `internal/runtime/runtimetest` | Implemented (M1) |
| Proxmox API client and fake server | `internal/proxmox`, `internal/proxmox/proxmoxtest` | Implemented (M1) |
| `proxmox-lxc` runtime | `internal/runtime/proxmoxlxc` | Implemented (M1) |
| Store (SQLite) | `internal/store` | Implemented (M2) |
| Event bus and recorder | `internal/events` | Implemented (M2) |
| Log store (files + follow) | `internal/logs` | Implemented (M2) |
| Scale set adapter (`actions/scaleset`) | `internal/github` | Implemented (M2) |
| Controller and reaper | `internal/controller` | Implemented (M2) |
| Ingest (TLS, per-environment tokens) | `internal/ingest` | Implemented (M2) |
| Agent | `cmd/ghrm-agent`, `internal/agent` | Implemented (M2) |
| REST API and SSE | `internal/api` | Implemented (M2, paging and heartbeats M3) |
| `ghrm version`, `smoke`, `serve`, `openapi`, `demo` | `cmd/ghrm` | Implemented (M1–M3) |
| Simulated fleet for UI work and browser tests | `internal/demo` | Implemented (M3) |
| Web UI (React, Kumo), embedded in the binary | `web/` | Implemented (M3); template pages in M4, sign-in and editable settings in M5 |
| Template builder (`ubuntu-slim`): build, verify, activate, retain, release checks | `internal/template`, `template/layer` | Implemented (M4); `deploy/proxmox/dev-template.sh` creates the bootstrap template |
| Agent build and self-test modes | `internal/agent` (`build.go`, `selftest.go`) | Implemented (M4) |
| UI auth, secrets, installer | `internal/auth`, `internal/secrets`, `deploy/` | Planned (M5) |

## 1. System overview

```mermaid
flowchart LR
    subgraph github["GitHub"]
        ss["Runner Scale Set API"]
        rest["REST API"]
    end

    subgraph cp["Control plane: ghrm (single Go binary)"]
        listener["Scale set listener"]
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

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef planned fill:#f1f3f5,stroke:#868e96,stroke-dasharray:5 5,color:#000
    class runtime,scheduler,listener,reaper,store,ingest,api,ui,templates done
```

## 1a. Web UI data flow

The UI is a single-page app embedded in `ghrm` (`web/embed.go`) and served for every non-API path. Server state is fetched over REST through a client generated from the OpenAPI document. One shared event stream keeps every page live by invalidating the queries an event affects; each open log view has its own stream.

```mermaid
flowchart LR
    subgraph browser["Operator browser"]
        pages["Pages: overview, jobs, environments,<br/>scale sets, live logs, settings"]
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
- Destroying an environment is the only mutating action. It needs the admin token, kept in `sessionStorage` until M5 adds sign-in, and a type-the-name confirmation.

## 2. Network and isolation

```mermaid
flowchart LR
    subgraph jobnet["Job network: SDN Simple zone, SNAT, DHCP, IPv4 only"]
        job["Job LXC (unprivileged)"]
        gw["SDN gateway (on the Proxmox host)"]
        cpjob["ghrm: job-network interface (ingest only)"]
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
    job ==>|"everything else, via SNAT"| internet
    job -. "blocked by the security group" .-x lan
    cplan --> hostapi
```

Security group applied to every job LXC (inherited from the template):

| Order | Direction | Rule |
|---|---|---|
| 1 | out | ACCEPT UDP 67 (DHCP) |
| 2 | out | ACCEPT TCP to the ingest address and port |
| 3 | in | DROP everything |
| 4 | out | DROP 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 |
| — | out | everything else (internet) is allowed |

Nothing can open a connection into a job LXC (rule 3). The ingest exception (rule 2) is the only internal destination a job can reach.

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
    GH-->>L: JobStarted / JobCompleted
    E->>I: job_finished, runner_exited, shutdown
    E->>E: power off
    C->>R: Destroy (stop if needed, delete)
    C->>GH: remove runner (if still registered)
    I-->>UI: every step is an event and a log line, streamed live
```

Agent → ingest frames are NDJSON batches every 250 ms. Each log stream has its own sequence numbers, so a retried batch is stored once. Events share the `agent` stream's sequence and reach the controller exactly once.

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
    configure --> settle["wait for pve-firewall (firewall_settle)"]
    settle --> ref(["return ref VMID/ID"])
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
    running -- no --> del["delete (purge)"]
    stop --> del
    stop -- "error: re-read and retry" --> look
    del -- "error: re-read and retry" --> look
    del --> done(["nil"])
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
    class ghrm,config,api,controller,ingest,github,proxmoxlxc,scheduler,environment,runtime,store,events,logs,proxmox,agentcmd,agent,ingestproto,demo,webui,template,layer done
    class runtimetest,proxmoxtest testonly
    class scaleset ext
```

Yellow packages are test doubles; `internal/demo` uses the fake runtime to serve a simulated fleet (`ghrm demo`). `cmd/ghrm-agent` shares only the wire protocol with the control plane.

## 7. Template pipeline

A build clones the **active template** into a builder environment (it already has Docker, systemd and `ghrm-agent`), because the Proxmox API cannot run commands inside a fresh stock container. The very first template comes from `deploy/proxmox/dev-template.sh` (the bootstrap template, `proxmox.template_vmid`).

```mermaid
flowchart LR
    rel["actions/runner-images release ubuntu-slim/*"] --> b1
    runner["actions/runner release + SHA-256"] --> b1
    layer["ghrm layer (template/layer, embedded in ghrm)"] --> b2
    subgraph builder["Builder LXC (clone of the active template, job network)"]
        b1["docker build: official ubuntu-slim Dockerfile, unmodified"] --> b2["docker build: ghrm layer (systemd, Docker Engine, runner, agent)"]
        b2 --> b3["docker export, drop container markers, zstd, SHA-256"]
    end
    b3 -- "PUT /ingest/v1/build/rootfs (streamed, size-capped)" --> cp["control plane: verify SHA-256"]
    cp --> up["upload to template storage (Proxmox verifies the SHA-256)"]
    up --> create["create LXC: unprivileged, nesting, keyctl, DNS, firewalled NIC, gh-runner group; convert to template"]
    create --> verify["verify LXC (clone): self-test + software report"]
    verify --> compare["compare with GitHub's published report"]
    compare -- "all checks pass, no unexpected differences, nothing pinned" --> active["active template"]
    compare -- "unexpected differences or pinned" --> ready["ready (manual activation)"]
    verify -- "a check fails" --> failed["failed: guests, template and archive removed"]

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    class rel,runner,layer,b1,b2,b3,cp,up,create,verify,compare,active,ready,failed done
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
    ready --> retired: beyond keep (default 2)
    retired --> deleted: no environment uses it
    building --> failed
    creating --> failed
    verifying --> failed: check failed, timeout, restart
    failed --> [*]
    deleted --> [*]
```

A failed build never changes the active template. Only one build runs at a time. The bootstrap template is never deleted.
