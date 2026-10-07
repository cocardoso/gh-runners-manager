#!/bin/bash
# Usage: persist-env.sh <environment file> VAR...
# docker export drops the image's ENV, so the layer keeps it in /etc/environment, where
# the agent reads it for jobs. The image's own scripts already write some of these names
# there with build-time placeholders (ImageOS= and ImageVersion=1.0.0); jobs on hosted
# runners see the container's ENV instead, so the image's values replace those lines.
# LANG is C.UTF-8 as on hosted images (sort order differs with other locales).
set -euo pipefail
file=$1
shift
drop() {
  local tmp
  tmp=$(mktemp)
  grep -v "^$1=" "$file" >"$tmp" || true
  cat "$tmp" >"$file"
  rm -f "$tmp"
}
for v in "$@"; do
  # Set, even to an empty value: a container's ENV wins over /etc/environment either way.
  if [ -n "${!v+x}" ]; then
    drop "$v"
    echo "${v}=${!v}" >>"$file"
  fi
done
drop LANG
echo 'LANG=C.UTF-8' >>"$file"
