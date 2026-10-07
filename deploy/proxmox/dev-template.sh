#!/usr/bin/env bash
# Bootstrap helper: builds the first job template with ghrm-agent from an existing
# runner template. ghrm then builds every later template itself from ubuntu-slim (M4),
# cloning its builder from the active template.
#
# Run on the Proxmox host:
#   dev-template.sh <source-template-vmid> <new-template-vmid> <path-to-ghrm-agent> <path-to-ghrm-agent.service (template/layer/ghrm-agent.service)> [pool]
set -euo pipefail

src=$1 new=$2 agent=$3 unit=$4 pool=${5:-ghrm}

if pct status "$new" >/dev/null 2>&1; then
  echo "VMID $new already exists" >&2
  exit 1
fi

echo "cloning $src -> $new"
pct clone "$src" "$new" --full 1 --hostname ghrm-template --pool "$pool"
pct set "$new" --tags "ghrm-template"
pct start "$new"
for _ in $(seq 1 30); do
  pct exec "$new" -- true 2>/dev/null && break
  sleep 1
done

pct push "$new" "$agent" /usr/local/bin/ghrm-agent --perms 0755
pct push "$new" "$unit" /etc/systemd/system/ghrm-agent.service --perms 0644
pct exec "$new" -- bash -euc '
  systemctl disable ghrm-runner.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/ghrm-runner.service /usr/local/bin/ghrm-entry
  systemctl daemon-reload
  systemctl enable ghrm-agent.service
  /usr/local/bin/ghrm-agent --environ /dev/null 2>/dev/null || true
  apt-get clean
  truncate -s 0 /etc/machine-id
  rm -f /var/lib/dbus/machine-id /etc/ssh/ssh_host_*
'
pct shutdown "$new" --timeout 60
pct template "$new"
echo "template $new ready"
