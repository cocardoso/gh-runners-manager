# Architecture

This document holds the architecture diagrams of gh-runners-manager (`ghrm`). It is updated in the same change as any code that alters components, flows, networking or states. The [design document](superpowers/specs/2026-10-07-gh-runners-manager-design.md) explains the reasoning behind the design.

Legend used in the diagrams: **green** = implemented, **grey dashed** = planned (with its milestone).

## Implementation status

| Component | Package / location | Status |
|---|---|---|
| Configuration | `internal/config` | Implemented (M1) |
| Environment state machine | `internal/environment` | Implemented (M1) |
| Capacity scheduler (pure logic) | `internal/scheduler` | Implemented (M1) |
| Runtime interface and in-memory fake | `internal/runtime`, `internal/runtime/runtimetest` | Implemented (M1) |
| Proxmox API client and fake server | `internal/proxmox`, `internal/proxmox/proxmoxtest` | Implemented (M1) |
| `proxmox-lxc` runtime | `internal/runtime/proxmoxlxc` | Implemented (M1) |
| `ghrm version`, `ghrm smoke` | `cmd/ghrm` | Implemented (M1) |
| Store (SQLite) and event bus | `internal/store`, `internal/events` | Planned (M2) |
| Scale set listener (`actions/scaleset`) | `internal/scaleset` | Planned (M2) |
| Controller and reaper | — | Planned (M2) |
| Agent and ingest | `cmd/ghrm-agent`, `internal/ingest` | Planned (M2) |
| REST API and SSE | `internal/api` | Planned (M2) |
| Web UI (React, Kumo) | `web/` | Planned (M3) |
| Template builder (`ubuntu-slim`) | `internal/template`, `template/layer` | Planned (M4) |
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
        store["Store: SQLite + log files"]
        ingest["Ingest"]
        api["REST API + SSE"]
        ui["Web UI (Kumo)"]
    end

    subgraph pve["Proxmox VE host"]
        pveapi["Proxmox API"]
        tmpl["Template LXC"]
        subgraph jobnet["Isolated job network"]
            env1["Job LXC + ghrm-agent"]
            env2["Job LXC + ghrm-agent"]
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
    ingest --> store
    scheduler --> store
    api --> store
    ui --> api
    operator --> ui

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef planned fill:#f1f3f5,stroke:#868e96,stroke-dasharray:5 5,color:#000
    class runtime,scheduler done
    class listener,reaper,store,ingest,api,ui planned
```

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
| 2 | out | ACCEPT TCP to the ingest address and port (planned, M2) |
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
    participant S as Scheduler
    participant R as Runtime (proxmox-lxc)
    participant P as Proxmox API
    participant E as Job LXC (ghrm-agent)
    participant I as Ingest

    GH-->>L: statistics: assigned jobs = N
    L->>S: desired environments
    S->>S: capacity check (memory, disk, limits)
    S->>GH: generate JIT runner config
    S->>R: Create(spec with JIT config in env)
    R->>P: nextid probe, linked clone, tag, configure (cores, memory, env)
    R->>R: wait for the firewall rules to apply
    S->>R: Start
    R->>P: start
    E->>I: hello, then logs and metrics
    E->>GH: runner online, takes one job
    GH-->>L: JobStarted / JobCompleted
    E->>I: job finished, exit code
    E->>E: power off
    S->>R: Destroy
    R->>P: stop (if needed), delete
```

Steps 4–8 and 10 exist today in `internal/runtime/proxmoxlxc` and are exercised by `ghrm smoke`. The listener, scheduler wiring, agent and ingest arrive in M2.

## 4. Environment states

Implemented in `internal/environment`. Every state with a timeout is enforced by the reaper (M2).

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
    main["cmd/ghrm"] --> config["internal/config"]
    main --> ids["internal/ids"]
    main --> proxmoxlxc["internal/runtime/proxmoxlxc"]
    proxmoxlxc --> runtime["internal/runtime"]
    proxmoxlxc --> proxmox["internal/proxmox"]
    runtimetest["internal/runtime/runtimetest"] --> runtime
    proxmoxtest["internal/proxmox/proxmoxtest"]
    scheduler["internal/scheduler"]
    environment["internal/environment"]

    classDef done fill:#d3f9d8,stroke:#2b8a3e,color:#000
    classDef testonly fill:#fff3bf,stroke:#e67700,color:#000
    class main,config,ids,proxmoxlxc,runtime,proxmox,scheduler,environment done
    class runtimetest,proxmoxtest testonly
```

`internal/scheduler` and `internal/environment` have no dependencies; the controller (M2) will connect them to the runtime and the store. Yellow packages are test doubles used only by tests.

## 7. Template pipeline (planned, M4)

```mermaid
flowchart LR
    rel["actions/runner-images release ubuntu-slim/*"] --> b1
    runner["actions/runner release"] --> b1
    layer["ghrm layer version"] --> b1
    subgraph builder["Builder LXC (temporary, job network)"]
        b1["docker build: official ubuntu-slim Dockerfile"] --> b2["docker build: ghrm layer (systemd, Docker, runner, agent)"]
        b2 --> b3["docker export: rootfs.tar.zst"]
    end
    b3 --> up["control plane uploads to Proxmox template storage"]
    up --> create["create template LXC (unprivileged, nesting, DNS, firewall group)"]
    create --> verify["verify: self-test + software report vs GitHub's report"]
    verify -- pass --> active["active template"]
    verify -- fail --> keep["keep the current active template"]

    classDef planned fill:#f1f3f5,stroke:#868e96,stroke-dasharray:5 5,color:#000
    class rel,runner,layer,b1,b2,b3,up,create,verify,active,keep planned
```
