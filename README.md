# gh-runners-manager

Ephemeral, isolated GitHub Actions runners on Proxmox LXC: one fresh environment per job, destroyed when the job ends, with a real-time web UI.

> **Status:** early development. Not ready for production use.

## How it works

- Jobs are received through GitHub's Runner Scale Set API (outbound connections only).
- Each job runs in an unprivileged LXC container, linked-cloned from a template that is built from GitHub's own `ubuntu-slim` image recipe.
- Job environments live on an isolated network that cannot reach your LAN or the hypervisor.
- A web UI shows queues, environments, jobs and the live logs of every lifecycle stage.

- [Architecture diagrams](docs/architecture.md): system overview, network isolation, job lifecycle, environment states and implementation status. Kept up to date with every change.
- [Design document](docs/superpowers/specs/2026-10-07-gh-runners-manager-design.md): the reasoning behind the design.
- [Development guide](docs/development.md): building, testing and running against a Proxmox host.

## Development

Requirements: Go 1.27+.

    make check   # vet, test (with -race) and build
    make build   # produces bin/ghrm
