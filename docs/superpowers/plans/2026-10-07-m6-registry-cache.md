# M6 — Registry Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pull-through registry cache on the job network that job environments use transparently (Docker daemon, containerd `hosts.toml`, BuildKit), with a disk budget, monitoring in ghrm, and installer support.

**Architecture:** A `ghrm-cache` LXC (installer-made) runs one CNCF Distribution proxy per origin (Docker Hub, GHCR, MCR, Quay), a prune timer that keeps it under its disk budget, and `ghrm-agent cache-exporter` for disk metrics. The ghrm layer writes the mirror configuration into templates when `cache.address` is set (part of the layer version). The control plane polls the cache, exposes `ghrm_cache_*` metrics, alerts when it is down, and shows a Cache card in Settings.

**Tech Stack:** Go 1.27, bash (layer scripts, prune, installer), CNCF Distribution v3 (`registry`), React + Kumo.

**Spec:** `docs/superpowers/specs/2026-10-07-m6-registry-cache-design.md` (and the main design for fidelity and isolation rules).

## Global Constraints

- Everything in the repository is in English; commits carry no AI attribution; no real homelab addresses in docs (examples use `10.50.0.0/24`, which is the documented default).
- No change to how workflows are written: the cache must be transparent (spec §1).
- Jobs may reach only the four mirror ports of the cache, never its metrics/exporter ports (spec §3.4).
- With `cache.address` empty, templates and behaviour are exactly as before M6.
- Every change to `template/layer` bumps `layer.Version`.
- Registry version pinned: Distribution `v3.1.2`, `registry_3.1.2_linux_amd64.tar.gz`, SHA-256 checked by the installer.
- `docs/architecture.md` diagrams updated in the same change set.

## Review Focus

1. **The cache is down while jobs run.** Expected: pulls fall back to the origin; jobs pass; ghrm shows the cache down. → Task 3 self-test treats an unreachable mirror as a failed check only during template verification, never during jobs; Task 8 real validation stops the cache and runs a job.
2. **Disk budget reached.** Expected: least recently used repositories are evicted until under the low mark; garbage collection runs; pulls keep working. → Task 4 `prune_evicts_least_recently_used_first`.
3. **A job tries the cache's metrics/exporter ports or other LAN addresses.** Expected: blocked. → Task 3 adds them to the self-test's blocked addresses.
4. **Cache enabled, disabled or moved.** Expected: the layer version changes, so a rebuild produces a template with (or without) the mirror files. → Task 2 `TestCacheChangesTheLayerVersion`.
5. **Installer re-run, or an existing security group without the cache rule.** Expected: no duplicate rule, cache container reused, credential never written to the host disk. → Task 7 tests.

---

### Task 1: `cache:` configuration

**Files:** `internal/config/config.go`, `internal/config/vault_test.go` (or a new `cache_test.go`), `deploy/examples/ghrm.example.yaml`.

**Interfaces — Produces:**
- `config.Cache{Address string; Ports map[string]int; MetricsPorts map[string]int; ExporterPort int}` at `Config.Cache` (`yaml:"cache"`).
- `(Cache) Enabled() bool` (address set).
- `(Cache) Mirrors() string` — canonical `origin=host:port` list sorted by origin, comma-separated (empty when disabled); used as the layer build argument and in the layer version.
- `(Cache) MirrorAddrs() []string`, `(Cache) PrivateAddrs() []string` (metrics + exporter `host:port`).
- Defaults when `address` is set: ports 5000–5003 and metrics 5100–5103 for `docker.io, ghcr.io, mcr.microsoft.com, quay.io`; exporter 5199. Validation: address is an IPv4 address; ports 1–65535 and distinct; only the four known origins.

- [ ] Failing tests: `TestCacheDefaultsAndMirrors`, `TestCacheValidation`, `TestCacheDisabledByDefault`; the example config test still passes with a `cache:` section.
- [ ] Implement; commit `feat(config): registry cache settings`.

### Task 2: Layer writes the mirror configuration

**Files:** create `template/layer/mirrors.sh`; modify `template/layer/Dockerfile`, `template/layer/embed.go` (embed + tar + `Version = "3"`), `template/layer/layer_test.go`; `internal/ingest/build.go` (`BuildSpec.CacheMirrors string`); `internal/agent/build.go` (`--build-arg CACHE_MIRRORS=`); `internal/agent/build_test.go`; `internal/template/service.go` (`layerVersion` hashes the agent SHA and `Cache.Mirrors()`; `BuildSpec` carries `CacheMirrors`); `internal/template/service_test.go`.

**Interfaces:** `mirrors.sh <root> <origin=host:port,...>` writes, under `<root>` (the image root in the Dockerfile, a temp dir in tests): `etc/docker/daemon.json` (`registry-mirrors` for docker.io, `insecure-registries` for all four), `etc/docker/certs.d/<origin>/hosts.toml` for the non-Docker Hub origins, `home/runner/.docker/buildx/buildkitd.default.toml` (mirrors + `http = true`), owned by `runner`; with an empty list it writes nothing.

- [ ] Failing tests: `TestMirrorsScriptWritesDaemonContainerdAndBuildkitConfig`, `TestMirrorsScriptWithoutCacheWritesNothing` (bash run like `persist-env.sh`), `TestTarHoldsTheLayerAndTheAgent` lists `mirrors.sh`, `TestRunBuild...` sees `--build-arg CACHE_MIRRORS=...`, `TestCacheChangesTheLayerVersion`, `TestBuildSpecCarriesTheCacheMirrors`.
- [ ] Implement; commit `feat(template): job templates use the registry cache`.

### Task 3: Self-test checks the cache and its isolation

**Files:** `internal/ingest/build.go` (`EnvSelfTestMirrors = "GHRM_SELFTEST_MIRRORS"`), `internal/agent/selftest.go` (+ test), `internal/template/service.go` (verify env: mirrors; blocked list gains `Cache.PrivateAddrs()`), `cmd/ghrm-agent/main.go`, `internal/agent/bootstrap.go` (parse the env).

**Interfaces:** `SelfTestOptions.Mirrors []string` (`host:port`); a check `registry cache` that GETs `http://<addr>/v2/` on each (any HTTP answer = reachable) — present only when mirrors are given.

- [ ] Failing tests: `TestSelfTestChecksTheCache` (one reachable, one not → failed check naming it), `TestVerifyEnvironmentGetsTheCache` (template service sets the env and blocks the private ports).
- [ ] Implement; commit `feat(template): verify the registry cache and keep its private ports closed to jobs`.

### Task 4: Cache container tools: prune script and disk exporter

**Files:** create `deploy/cache/ghrm-cache-prune` (bash), `deploy/cache/test/run.sh`; create `internal/agent/cacheexporter.go` (+ test); modify `cmd/ghrm-agent/main.go` (`ghrm-agent cache-exporter --listen :5199 --status /var/lib/ghrm-cache/status`).

**Interfaces:**
- `ghrm-cache-prune` reads `/etc/ghrm-cache/prune.conf` (`ROOT`, `BUDGET_BYTES`, `HIGH_PERCENT=85`, `LOW_PERCENT=70`, `INSTANCES` list of `name:config`), measures usage with `du -sb "$ROOT"`, evicts repositories (`<root>/<origin>/docker/registry/v2/repositories/<repo>`) oldest access first (newest blob/link `atime`/`mtime` under the repository) until below LOW, runs `registry garbage-collect --delete-untagged <config>` for each touched instance (stop/start the instance around it), and writes `used_bytes N\nbudget_bytes M\n` to the status file. Env overrides for tests: `DU`, `REGISTRY`, `SYSTEMCTL`.
- `agent.CacheExporter(statusPath string) http.Handler` serving `ghrm_cache_disk_used_bytes` and `ghrm_cache_disk_budget_bytes` gauges (missing file → 503).

- [ ] Failing tests: shell `prune_does_nothing_under_the_high_mark`, `prune_evicts_least_recently_used_first`, `prune_runs_garbage_collection_for_touched_instances`, `prune_writes_the_status`; Go `TestCacheExporterServesTheStatus`, `TestCacheExporterWithoutStatusIsUnavailable`.
- [ ] Implement; CI runs the prune tests (installer job); commit `feat(cache): eviction under a disk budget and a disk exporter`.

### Task 5: Cache monitor in the control plane

**Files:** create `internal/cachemon/monitor.go` (+ test); modify `internal/metrics/metrics.go`, `internal/api/overview.go` (alert + `Settings.cache`), new `GET /api/v1/cache` in `internal/api`, `cmd/ghrm/serve.go`, demo (a simulated cache status so the UI and e2e have one).

**Interfaces:**
- `cachemon.Monitor{Cache config.Cache; HTTP *http.Client; Recorder; Now}` with `Run(ctx)` (every 30 s) and `Status() cachemon.Status{Enabled, Up bool; Origins []OriginStatus{Origin, Up bool, Hits, Misses float64}; DiskUsed, DiskBudget int64; CheckedAt time.Time}`.
- Origin metrics parsed from each instance's `/metrics` (Prometheus text): Distribution's proxy counters (`registry_proxy_hits_total`/`registry_proxy_misses_total` for `type="blob"`; exact names confirmed against the real binary in this task and asserted by a fixture captured from it).
- Events `cache.down` / `cache.up` on transitions; `ghrm_cache_up`, `ghrm_cache_requests_total{origin,result}`, `ghrm_cache_disk_bytes{kind}`; overview alert `cache_down`.

- [ ] Failing tests: `TestMonitorReadsOriginsAndDisk` (fake servers with captured fixtures), `TestMonitorRecordsDownAndUp`, `TestCacheMetrics`, API `TestCacheEndpoint`, overview `TestCacheDownAlert`.
- [ ] Implement; `make web-api`; commit `feat(cache): monitor the registry cache`.

### Task 6: Cache card in Settings

**Files:** `web/src/api/queries.ts` (`useCache`), `web/src/components/cache-card.tsx` (+ test), `web/src/pages/settings.tsx`, overview alert text; e2e screenshot of Settings refreshed.

- Card: Not configured (with a hint) / per-origin state badges, hits, hit ratio, disk used of budget meter, last check time.
- [ ] Failing tests: `cache card shows origins, hit ratio and disk`, `cache card explains when no cache is configured`; implement; commit `feat(web): registry cache status`.

### Task 7: Installer: the cache container

**Files:** `deploy/proxmox/install.sh`, `deploy/proxmox/test/run.sh`, `deploy/proxmox/test/fakebin/fake-pve`.

- New options: `--cache-vmid`, `--cache-disk-gb` (100), `--no-cache`, `--dockerhub-user` (token from standard input when given, with a prompt only on a terminal).
- Steps: container `ghrm-cache` (tag `ghrm-cache`, `<subnet>.3`, not in the pool, onboot), registry binary (pinned + SHA-256), four instances + units, prune script + config + timer, exporter (agent binary) service, Docker Hub credential written only inside the container (0600), firewall rule `OUT ACCEPT tcp <cache> 5000:5003` placed before the private-range drops (added to an existing group when missing), the `cache:` section appended to `ghrm.yaml` when missing (restart ghrm).
- The embedded prune script equals `deploy/cache/ghrm-cache-prune` (test).
- [ ] Failing tests: `cache_is_created`, `cache_rerun_changes_nothing`, `existing_group_gets_the_cache_rule_once`, `no_cache_skips_it`, `dockerhub_token_never_touches_the_host_disk`, `embedded_prune_matches_the_repository`; implement; commit `feat(deploy): the installer sets up the registry cache`.

### Task 8: Docs, diagrams and real validation

- `docs/architecture.md` (overview, network diagram and security-group table, deployment), `docs/development.md` (cache section: what is cached, budget, Docker Hub credential, how to disable), README note.
- Real validation on the development host: run the installer (non-dry, `--vmid 310 --template-vmid 951 --security-group gh-runner --cache-vmid 320 --binary-dir`), rebuild the template, then real jobs through a temporary `ghrm-e2e` branch in zeropaper: Playwright `container:`, `services: redis`, `setup-buildx-action` + `build-push-action` with a multi-registry Dockerfile; compare a cold and a warm run; check the cache metrics in ghrm; stop the cache and run a job (falls back); set a tiny budget and watch eviction; delete the temporary branch.
- [ ] Commit `docs: registry cache`.

### Final: independent review (opus), all findings fixed with RED→GREEN tests, PR, CI, merge.
