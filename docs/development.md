# Development

## Build and test

    make check

## Requirements on the Proxmox host

- Proxmox VE **9.1 or later**: ghrm injects the runner bootstrap through the LXC `env` option, which older versions do not have.
- An LVM-thin (or other snapshot-capable) storage for linked clones.

## Installing on a Proxmox host

`deploy/proxmox/install.sh` does everything below in one run (as root on the host): the pool, roles, user and token, the template storage, the job network and its security group, the control-plane container with ghrm, and a bootstrap template. It checks every step first, so running it again resumes or upgrades; `--dry-run` shows what would change and `--help` lists the options (VMIDs, bridges, subnet, storages, release). It ends with the web UI address and the one-time setup token for the admin account. Its tests (`bash deploy/proxmox/test/run.sh`) run it against fake Proxmox tools. Release binaries carry signed build provenance: `gh attestation verify ghrm-linux-amd64 --repo cocardoso/gh-runners-manager`.

Releasing: push a `vX.Y.Z` tag from `main`. The release workflow publishes the binaries, `install.sh`, `SHA256SUMS` and the container image (`ghcr.io/cocardoso/gh-runners-manager`). To upgrade a host, run the release's installer again (`bash install.sh`, or `--version vX.Y.Z` for a given release): it replaces ghrm and the agents and keeps the configuration and data; ghrm rebuilds the job template when the agent changed.

The sections below describe the same setup by hand.

## Scoped Proxmox API token

ghrm never uses `root`. Create a dedicated user, role, pool and token on the Proxmox host. Replace the template VMID, storage, SDN zone and node names with yours:

    pveum pool add ghrm --comment "gh-runners-manager environments"
    pveum role add GhrmRuntime --privs "VM.Allocate VM.Clone VM.Audit VM.PowerMgmt VM.Config.CPU VM.Config.Memory VM.Config.Disk VM.Config.Network VM.Config.Options Datastore.AllocateSpace Datastore.Audit SDN.Use Sys.Audit Pool.Audit"
    pveum user add ghrm@pve --comment "gh-runners-manager"
    pveum acl modify /pool/ghrm --users ghrm@pve --roles GhrmRuntime
    pveum acl modify /storage/local-lvm --users ghrm@pve --roles GhrmRuntime
    pveum acl modify /sdn/zones/runners --users ghrm@pve --roles GhrmRuntime
    pveum acl modify /nodes/pve --users ghrm@pve --roles GhrmRuntime
    pveum pool modify ghrm --vms 9000          # the template must be in the pool
    pveum user token add ghrm@pve ghrm --privsep 0
    # Template builds: a dedicated storage for the archives, so deleting them (Datastore.Allocate)
    # cannot touch anything else. Templates are created in the pool (tags are set right after
    # creation, because Proxmox checks tag permissions before a new container joins the pool).
    mkdir -p /var/lib/ghrm-templates
    pvesm add dir ghrm-tpl --path /var/lib/ghrm-templates --content vztmpl
    pveum role add GhrmTemplates --privs "Datastore.AllocateTemplate Datastore.Allocate Datastore.Audit"
    pveum acl modify /storage/ghrm-tpl --users ghrm@pve --roles GhrmTemplates

Store the printed secret in the control plane's vault: `ghrm secret set proxmox/token-secret` (it reads the value from standard input). A file named by `proxmox.token_secret_file` (mode `0600`) or `GHRM_PROXMOX_TOKEN_SECRET` also work, and win over the vault.

## Secrets

Secrets are sealed in the database with AES-256-GCM; the key lives in `secret_key_file` (default `<data_dir>/secret.key`, created `0600` on first use, refused when others can read it). Back the key up with the database: without it the sealed secrets cannot be read, and ghrm refuses to start with a different key rather than run without them.

- `ghrm secret set|delete|list [--config path] [name]` manages them; `list` never prints values.
- Names: `proxmox/token-secret`, and `github/<credential>` for GitHub tokens (the UI writes those).
- Precedence for each secret: environment variable, then file, then vault.

## Sign-in

The web UI and the API need a signed-in session. On first start ghrm writes a one-time setup token to `<data_dir>/setup-token` (and logs it); the first visitor creates the admin account with it, and the file is removed. Passwords are hashed with argon2id; sessions last seven days from their last use, live in HttpOnly, SameSite=Strict cookies (Secure behind HTTPS, via `X-Forwarded-Proto` from a reverse proxy), and state-changing requests need the session's CSRF token. Five failed sign-ins from one address lock it out for a minute, doubling up to an hour. Scripts can use `Authorization: Bearer <admin token>` (from `admin_token_file`). Every administrative action is an `audit.*` event with its actor.

Recommended exposure: the LAN only, or behind an identity-aware proxy.

The role does not include `Sys.Audit` on `/`, so the token cannot read the thin pool's metadata usage. ghrm then reports disk usage from the storage status (`proxmox.storage`, data usage only). Grant `Sys.Audit` on `/` if you also want metadata usage checked.

`proxmox.pool` is required in the configuration: new environments are created in that pool, which is where the token has its permissions.

## TLS

Proxmox uses a self-signed certificate by default. Pin it with `proxmox.tls_fingerprint` (SHA-256, as shown in the Proxmox UI under the node's certificates) instead of setting `insecure_skip_verify`.

## Smoke test against a real host

The template must be an LXC template (`pct template <vmid>`) attached to the job network, with its firewall enabled and the job security group applied.

    make build
    ./bin/ghrm smoke --config ghrm.yaml

Expected output: the host capacity, then creation (about 12–15 s including the firewall settle), start, a running IP on the job network, and destruction.

## Development deployment

To try a branch on a host that already has the setup (for example one made by the installer), build with `make build-linux`, copy `dist/linux-amd64/ghrm` and `ghrm-agent` to the host, stop the service in the control-plane container, `pct push` both binaries to `/usr/local/bin`, and start it again. `install.sh --binary-dir <dir>` does the same, and creates whatever is missing.

GitHub credentials (a fine-grained PAT with **Administration: read and write** on the repository; **Actions: read** shows job steps; for an organization's scale set, the organization permission **Self-hosted runners: read and write**) and scale sets are added in the UI (Settings, Scale sets), or in `ghrm.yaml`. Workflows use `runs-on: <scale set name>`.

Useful API calls (with the admin token from `/etc/ghrm/admin-token`):

    export H="Authorization: Bearer $(cat /etc/ghrm/admin-token)"
    curl -H "$H" http://<ghrm>:8080/api/v1/scale-sets
    curl -H "$H" http://<ghrm>:8080/api/v1/environments
    curl -H "$H" -N 'http://<ghrm>:8080/api/v1/events/stream'
    curl -H "$H" -N 'http://<ghrm>:8080/api/v1/environments/<id>/logs/job?follow=true'
    curl -H "$H" -X POST http://<ghrm>:8080/api/v1/templates/build

## Operations

- `/healthz` and `/readyz` (the store and the Proxmox API answer), unauthenticated.
- `/metrics` (Prometheus, unauthenticated): `ghrm_environments{scale_set,state}`, `ghrm_environment_failures_total{stage}`, `ghrm_jobs_total{scale_set,result}`, `ghrm_scale_set_desired` (queue depth) and `ghrm_scale_set_listening`, `ghrm_template_builds_total{result}`, `ghrm_stage_duration_seconds{stage}`, `ghrm_build_info{version}`, plus Go process metrics.
- Backups: every day at `backup.hour` (default 3, local time) ghrm writes a consistent copy of the database with `VACUUM INTO` to `backup.dir` (default `<data_dir>/backups`, mode `0600`) and keeps `backup.keep` copies (default 7). Each run is a `backup.done` or `backup.failed` event. Keep `secret.key` with them, and back up the container with Proxmox backups too.

## History

Finished environments (with their jobs, events and log files), events and failed or deleted template records are history. Settings, History chooses how it is cleaned:

- **Automatic** (default): every day at half past `backup.hour`, history older than the configured days (default 30) is deleted, and audit events (`audit.*`: sign-ins and administrators' changes) older than their own period (default 365 days, never shorter than the history). Each sweep is a `retention.cleaned` (or `retention.failed`) event with the counts, and the database is compacted afterwards.
- **Manual**: nothing is deleted until someone uses **Clean up now…**, which shows how much would go before it deletes. It works in both modes.
- Never deleted: environments that are not destroyed, the build and verification environments of templates that are kept (their logs are those templates' records), templates other than failed or deleted, and settings, credentials and accounts.
- A failed or deleted template record, or a destroyed environment, can also be deleted on its own page. Deleting a failed template lets the next check build the same inputs again.
- API: `GET`/`PUT /api/v1/history/settings`, `POST /api/v1/history/cleanup` (`before`, optional `audit_before`, `dry_run`), `DELETE /api/v1/templates/{id}`, `DELETE /api/v1/environments/{id}`. Manual deletions are audited.

## Registry cache

With `cache.address` set (the installer sets it up and creates the container), job environments pull Docker Hub, GHCR, MCR and Quay images through a pull-through cache on the job network (`<subnet>.3`), with unchanged workflows. The cache is an unprivileged Debian container (`ghrm-cache`, not in the ghrm pool) running one CNCF Distribution proxy per registry on ports 5000–5003, their metrics on 5100–5103 and `ghrm-agent cache-exporter` on 5199; jobs can reach only 5000–5003.

- Disk: `install.sh --cache-disk-gb N` (default 100; on a re-run the existing disk is kept, and a larger value grows it, since Proxmox cannot shrink it). Deleted files are given back to the thin pool (`discard`). Eviction keeps the cache under 90 % of it: above 85 % of that budget the least recently used repositories are deleted and the registry's garbage collection runs (`ghrm-cache-prune.timer`, every 15 minutes) until usage is below 70 % of the budget. Each eviction is logged (`journalctl -u ghrm-cache-prune` in the cache container); the disk use shown in Settings is the one the last run measured.
- Docker Hub limits anonymous pulls per address, and every job leaves through the same address. `install.sh --dockerhub-user NAME` (access token on standard input) configures an account in the cache only. Every job can then pull what that account can, private repositories included, so use an access token with the **Public Repo Read-only** scope. A re-run keeps the account; `--dockerhub-clear` removes it.
- Tags are checked with the registry on every pull; a cached tag is served only when the registry cannot be reached.
- Settings shows the cache (each registry, the share served from the cache, disk use); the overview alerts after two failed checks in a row (one minute), and jobs then pull from the registries directly. A template verified while the cache is down is still activated: its cache check passes with a warning.
- To disable it, remove the `cache:` section (templates are rebuilt without the mirror settings), or install with `--no-cache`.
- What is not cached: GitHub's Actions cache (`actions/cache`, `setup-node`'s `cache:`, BuildKit `type=gha`) and package registries (npm, PyPI, apt).

## UI languages

The interface text lives in `web/src/i18n/areas/<area>.ts`, one file per area of the UI with every language in it; English is the source, and TypeScript fails when another language misses or adds a key. Components use `useT()` (or `tr()` outside React), and dates and numbers go through `Intl` in the chosen language. Text that comes from the server (events, failure reasons, logs, API errors) is shown as it is. The browser's language decides (`pt*`, `es*`, `fr*`, `it*`, otherwise English) until someone picks one in the language menu, which this browser remembers.

- `pnpm lint` reports English written straight into JSX (`eslint-rules/no-jsx-literals.js`; product names and code examples are listed in `eslint.config.js`).
- `src/i18n/parity.test.ts` checks that every language has every key, with the same `{parameters}`, and no sentence left in English.
- Adding a language: add it to `web/src/i18n/locales.ts` (with its name in its own language), add its block to every area file, and run the tests.

## Docker

`docker build -t ghrm .` builds the control plane image (the web UI embedded, plus `ghrm-agent` for template builds; distroless, non-root). `deploy/docker/compose.yaml` runs it against a remote Proxmox host: put `ghrm.yaml` next to it with `data_dir: /var/lib/ghrm`, `listen: 0.0.0.0:8080`, `ingest.listen: 0.0.0.0:8443`, and an `ingest.advertise_url` that job environments can reach (allow it in the job security group), then store the secrets with `docker compose run --rm ghrm secret set proxmox/token-secret` and start it with `docker compose up -d`.

## Web UI

The UI lives in `web/` (Vite, React, TypeScript, Tailwind CSS v4 and Kumo). `make web` builds it into `web/dist`, which `web/embed.go` embeds into the `ghrm` binary; the API serves it for every non-API path, with an SPA fallback and long cache headers for hashed assets.

- `make web-api` regenerates `web/src/api/schema.d.ts` from `ghrm openapi` after an API change.
- `go run ./cmd/ghrm demo` serves the real API backed by a simulated fleet (fake runtime and GitHub). Flags: `--listen`, `--seed`, `--tick`, `--job-seconds min-max`, `--data-dir`. Sign in as `admin` / `demo-password`; the API token is `demo`.
- `cd web && pnpm dev` serves the UI with hot reload and proxies `/api` to `GHRM_API` (default `http://127.0.0.1:8080`).
- `pnpm lint`, `pnpm typecheck` and `pnpm test` run ESLint, TypeScript and Vitest. Component tests fail on any Kumo console warning.
- `pnpm e2e` builds `ghrm` with the current `web/dist` and runs the Playwright browser tests against `ghrm demo`, including a control-plane restart and a phone viewport.
- `pnpm screenshots` refreshes the README screenshots in `docs/images`.

## Template builds

With `templates.vmid_range` set, ghrm builds templates itself (spec §8): a builder environment, cloned from the active template, builds GitHub's official `ubuntu-slim` image unmodified, then the ghrm layer in `template/layer`; the control plane uploads the root filesystem to Proxmox, creates and converts the template, and verifies a clone (Docker, buildx, compose, DNS, HTTPS, blocked addresses, the runner binary and the software report).

- The first template is the bootstrap template (`proxmox.template_vmid`), made by the installer.
- `ghrm-agent` must sit next to the `ghrm` binary (or set `templates.agent_path`): builders receive it with the layer.
- Change any file in `template/layer` together with `layer.Version`; a new version triggers a rebuild.
- A build needs about 10 GB of free space in the builder (`templates.builder_disk_gb`, default 48) and room for the archive in the control plane's `data_dir` until it is uploaded.
- `templates.selftest_blocked` lists `host:port` addresses that answer on the LAN (for example the Proxmox API and SSH); the self-test fails if a job can reach any of them.
- On LVM-thin, a linked clone's configuration does not name its base volume, so ghrm knows which template an environment uses from its own records (`template_vmid` of each environment); the hypervisor check only helps on ZFS and directory storages. Deleting an LVM-thin template does not break existing clones.
- A guest that powers itself off is given a few seconds to unmount before it is deleted: deleting it at once can fail half-way after Proxmox has already removed it from the pool, leaving a guest the token can no longer see (remove such a guest as root with `pct destroy <vmid> --purge`).
- A Proxmox task that succeeds with warnings is logged and recorded as a `proxmox.task_warnings` event. A half-failed destroy can also leave the guest's DHCP reservation in `/etc/dnsmasq.d/<zone>/ethers` after its IP is released: the next guest given that IP gets no DHCP answer (dnsmasq logs `duplicate dhcp-host IP address` and ignores its DHCPDISCOVER), so its agent never says hello and the environment times out in `booting`. Remove the line of the MAC that no guest uses any more, then `systemctl reload dnsmasq@<zone>`.

