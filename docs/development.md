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

`proxmox.pool` is required in the configuration: new environments are created in that pool, which is where the token has its permissions.

## TLS

Proxmox uses a self-signed certificate by default. Pin it with `proxmox.tls_fingerprint` (SHA-256, as shown in the Proxmox UI under the node's certificates) instead of setting `insecure_skip_verify`.

## Smoke test against a real host

The template must be an LXC template (`pct template <vmid>`) attached to the job network, with its firewall enabled and the job security group applied.

    make build
    ./bin/ghrm smoke --config ghrm.yaml

Expected output: the host capacity, then creation (about 12–15 s including the firewall settle), start, a running IP on the job network, and destruction.
