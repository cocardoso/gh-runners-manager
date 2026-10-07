# Development

## Build and test

    make check

## Requirements on the Proxmox host

- Proxmox VE **9.1 or later**: ghrm injects the runner bootstrap through the LXC `env` option, which older versions do not have.
- An LVM-thin (or other snapshot-capable) storage for linked clones.

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

Store the printed secret in `/etc/ghrm/proxmox-token` (mode `0600`) or export it as `GHRM_PROXMOX_TOKEN_SECRET`.

The role does not include `Sys.Audit` on `/`, so the token cannot read the thin pool's metadata usage. ghrm then reports disk usage from the storage status (`proxmox.storage`, data usage only). Grant `Sys.Audit` on `/` if you also want metadata usage checked.

`proxmox.pool` is required in the configuration: new environments are created in that pool, which is where the token has its permissions.

## TLS

Proxmox uses a self-signed certificate by default. Pin it with `proxmox.tls_fingerprint` (SHA-256, as shown in the Proxmox UI under the node's certificates) instead of setting `insecure_skip_verify`.

## Smoke test against a real host

The template must be an LXC template (`pct template <vmid>`) attached to the job network, with its firewall enabled and the job security group applied.

    make build
    ./bin/ghrm smoke --config ghrm.yaml

Expected output: the host capacity, then creation (about 12–15 s including the firewall settle), start, a running IP on the job network, and destruction.

## End-to-end deployment on Proxmox (development)

Until the installer (M5) and the template builder (M4) exist, a development deployment is assembled by hand. Addresses below are examples.

1. **Job network and security group.** An SDN Simple zone with SNAT and DHCP (for example `10.50.0.0/24`, gateway `.1`, DHCP `.100–.199`). Security group rules for job guests, in this order:
   1. OUT ACCEPT TCP to the control plane's job-network address on port 8443 (ingest);
   2. OUT ACCEPT UDP 67;
   3. IN DROP;
   4. OUT DROP 169.254.0.0/16, 192.168.0.0/16, 172.16.0.0/12 and 10.0.0.0/8.
2. **Job template with the agent.**
   1. Build the binaries with `make build-linux`.
   2. On the Proxmox host, run `deploy/proxmox/dev-template.sh <source-template> <new-template> dist/linux-amd64/ghrm-agent template/layer/ghrm-agent.service ghrm`. The source template must already contain the GitHub runner in `/home/runner/actions-runner`, Docker, and the job security group on its NIC.
3. **Control-plane LXC.** A small Debian container with two NICs:
   - one on the LAN, used for the UI/API and to reach the Proxmox API;
   - one on the job network at the ingest address.

   Install `ghrm` to `/usr/local/bin`, `deploy/systemd/ghrm.service`, and `/etc/ghrm/ghrm.yaml` (see `deploy/examples/ghrm.example.yaml`) with its secret files (mode `0600`).
4. **GitHub.** A fine-grained PAT with **Administration: read and write** on the repository is enough for a repository-level scale set. `ghrm serve` creates the scale set on first start.
5. **Workflow.** Use `runs-on: <scale set name>`.

Useful API calls:

    curl http://<ghrm>:8080/api/v1/scale-sets
    curl http://<ghrm>:8080/api/v1/environments
    curl -N 'http://<ghrm>:8080/api/v1/events/stream'
    curl -N 'http://<ghrm>:8080/api/v1/environments/<id>/logs/job?follow=true'

## Web UI

The UI lives in `web/` (Vite, React, TypeScript, Tailwind CSS v4 and Kumo). `make web` builds it into `web/dist`, which `web/embed.go` embeds into the `ghrm` binary; the API serves it for every non-API path, with an SPA fallback and long cache headers for hashed assets.

- `make web-api` regenerates `web/src/api/schema.d.ts` from `ghrm openapi` after an API change.
- `go run ./cmd/ghrm demo` serves the real API backed by a simulated fleet (fake runtime and GitHub). Flags: `--listen`, `--seed`, `--tick`, `--job-seconds min-max`, `--data-dir`. The admin token is `demo`.
- `cd web && pnpm dev` serves the UI with hot reload and proxies `/api` to `GHRM_API` (default `http://127.0.0.1:8080`).
- `pnpm lint`, `pnpm typecheck` and `pnpm test` run ESLint, TypeScript and Vitest. Component tests fail on any Kumo console warning.
- `pnpm e2e` builds `ghrm` with the current `web/dist` and runs the Playwright browser tests against `ghrm demo`, including a control-plane restart and a phone viewport.
- `pnpm screenshots` refreshes the README screenshots in `docs/images`.

## Template builds

With `templates.vmid_range` set, ghrm builds templates itself (spec §8): a builder environment, cloned from the active template, builds GitHub's official `ubuntu-slim` image unmodified, then the ghrm layer in `template/layer`; the control plane uploads the root filesystem to Proxmox, creates and converts the template, and verifies a clone (Docker, buildx, compose, DNS, HTTPS, blocked addresses, the runner binary and the software report).

- The first template is the bootstrap template (`proxmox.template_vmid`), made with `deploy/proxmox/dev-template.sh`. Its `ghrm-agent` must support the build mode, so recreate it with the current agent when upgrading from M3.
- `ghrm-agent` must sit next to the `ghrm` binary (or set `templates.agent_path`): builders receive it with the layer.
- Change any file in `template/layer` together with `layer.Version`; a new version triggers a rebuild.
- A build needs about 10 GB of free space in the builder (`templates.builder_disk_gb`, default 48) and room for the archive in the control plane's `data_dir` until it is uploaded.
- `templates.selftest_blocked` lists `host:port` addresses that answer on the LAN (for example the Proxmox API and SSH); the self-test fails if a job can reach any of them.
- On LVM-thin, a linked clone's configuration does not name its base volume, so ghrm knows which template an environment uses from its own records (`template_vmid` of each environment); the hypervisor check only helps on ZFS and directory storages. Deleting an LVM-thin template does not break existing clones.
- A guest that powers itself off is given a few seconds to unmount before it is deleted: deleting it at once can fail half-way after Proxmox has already removed it from the pool, leaving a guest the token can no longer see (remove such a guest as root with `pct destroy <vmid> --purge`).
- A Proxmox task that succeeds with warnings is logged and recorded as a `proxmox.task_warnings` event. A half-failed destroy can also leave the guest's DHCP reservation in `/etc/dnsmasq.d/<zone>/ethers` after its IP is released: the next guest given that IP gets no DHCP answer (dnsmasq logs `duplicate dhcp-host IP address` and ignores its DHCPDISCOVER), so its agent never says hello and the environment times out in `booting`. Remove the line of the MAC that no guest uses any more, then `systemctl reload dnsmasq@<zone>`.

