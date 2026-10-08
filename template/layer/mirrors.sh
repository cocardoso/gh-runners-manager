#!/bin/bash
# Usage: mirrors.sh <root> <origin=host:port,...>
# Points job environments at the registry cache on the job network, so images are
# pulled through it without any workflow change: the Docker daemon (registry-mirrors,
# Docker Hub only), containerd's hosts.toml (the other registries) and BuildKit's default
# configuration for buildx builders. The cache is plain HTTP inside the isolated job
# network; content is addressed by digest. With an empty list nothing is written.
set -euo pipefail
root=${1%/}
list=${2:-}
[ -n "$list" ] || exit 0
# This script owns daemon.json; settings from elsewhere would be lost without a word.
if [ -e "$root/etc/docker/daemon.json" ]; then
  echo "mirrors.sh: $root/etc/docker/daemon.json already exists; merge the cache settings into it by hand" >&2
  exit 1
fi

hub="" insecure="" buildkit=""
IFS=',' read -r -a entries <<<"$list"
for e in "${entries[@]}"; do
  origin=${e%%=*} addr=${e#*=}
  insecure="$insecure${insecure:+, }\"$addr\""
  buildkit="$buildkit[registry.\"$origin\"]
  mirrors = [\"$addr\"]
[registry.\"$addr\"]
  http = true
"
  if [ "$origin" = docker.io ]; then
    hub=$addr
  else
    mkdir -p "$root/etc/docker/certs.d/$origin"
    cat >"$root/etc/docker/certs.d/$origin/hosts.toml" <<TOML
server = "https://$origin"

[host."http://$addr"]
  capabilities = ["pull", "resolve"]
TOML
  fi
done

mkdir -p "$root/etc/docker"
{
  echo "{"
  if [ -n "$hub" ]; then echo "  \"registry-mirrors\": [\"http://$hub\"],"; fi
  echo "  \"insecure-registries\": [$insecure]"
  echo "}"
} >"$root/etc/docker/daemon.json"

# For the runner user and for root (sudo docker buildx create).
for home in home/runner root; do
  mkdir -p "$root/$home/.docker/buildx"
  printf '%s' "$buildkit" >"$root/$home/.docker/buildx/buildkitd.default.toml"
done
if id runner >/dev/null 2>&1; then chown -R runner:runner "$root/home/runner/.docker"; fi
