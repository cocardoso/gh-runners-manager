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
   2. On the Proxmox host, run `deploy/proxmox/dev-template.sh <source-template> <new-template> dist/linux-amd64/ghrm-agent deploy/systemd/ghrm-agent.service ghrm`. The source template must already contain the GitHub runner in `/home/runner/actions-runner`, Docker, and the job security group on its NIC.
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
