#!/bin/bash
# toolcache.sh TOOL VERSION...: installs the newest release of each VERSION prefix of TOOL
# (node, python or go) into the hosted tool cache, as GitHub-hosted runners have it, so
# actions/setup-<tool> finds it instead of downloading it in every job.
# Tests: TOOLCACHE_MANIFEST reads the manifest from a file, TOOLCACHE_PRINT=1 prints the
# chosen URLs instead of installing, TOOLCACHE_OS overrides the Ubuntu version.
set -euo pipefail
tool=$1
shift
export AGENT_TOOLSDIRECTORY=${AGENT_TOOLSDIRECTORY:-/opt/hostedtoolcache}
VERSION_ID=${TOOLCACHE_OS:-}
[ -n "$VERSION_ID" ] || . /etc/os-release
if [ -n "${TOOLCACHE_MANIFEST:-}" ]; then
  manifest=$(cat "$TOOLCACHE_MANIFEST")
else
  manifest=$(curl -fsSL --retry 3 "https://raw.githubusercontent.com/actions/$tool-versions/main/versions-manifest.json")
fi
for want in "$@"; do
  url=$(jq -r --arg want "$want" --arg os "$VERSION_ID" '
    [.[] | select(.stable != false)
         | select(.version == $want or (.version | startswith($want + ".")))
         | .files[] | select(.platform == "linux" and .arch == "x64")
         | select((.platform_version // $os) == $os)
         | .download_url][0] // empty' <<<"$manifest")
  if [ -z "$url" ]; then
    echo "toolcache: no $tool $want for linux x64 (Ubuntu $VERSION_ID)" >&2
    exit 1
  fi
  if [ -n "${TOOLCACHE_PRINT:-}" ]; then
    echo "$url"
    continue
  fi
  dir=$(mktemp -d)
  curl -fsSL --retry 3 "$url" | tar xz -C "$dir"
  (cd "$dir" && bash ./setup.sh)
  rm -rf "$dir"
done
