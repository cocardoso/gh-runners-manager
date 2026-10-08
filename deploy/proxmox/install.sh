#!/usr/bin/env bash
# gh-runners-manager installer for a Proxmox VE host (spec §12.1).
#
# Run it as root in the Proxmox host shell. It is idempotent: every step checks what
# exists and creates only what is missing, so it can be run again (to resume after an
# error, or to upgrade ghrm to a new release). Use --dry-run to see what it would do.
#
#   curl -fsSLO https://github.com/cocardoso/gh-runners-manager/releases/latest/download/install.sh
#   bash install.sh --lan-ip dhcp
#
# What it sets up:
#   - a resource pool, two roles, a user and an API token for ghrm (never root);
#   - a dedicated storage for template archives;
#   - the job network: an SDN Simple zone with DHCP and SNAT, and a security group that
#     lets jobs reach only the internet and the control plane's ingest port;
#   - the control-plane container (Debian 13) running ghrm as a systemd service;
#   - a bootstrap job template (Ubuntu 24.04 with Docker, the GitHub runner and
#     ghrm-agent), which ghrm uses to build the real templates from GitHub's ubuntu-slim.
# Then it prints the web UI address and the one-time setup token.
set -euo pipefail

REPO=cocardoso/gh-runners-manager

# --- options ---------------------------------------------------------------------------
NODE=$(hostname)
CT_VMID=""               # control plane; default: the existing one, else the next free ID
TEMPLATE_VMID=""         # bootstrap template; default: the existing one, else the next free ID
POOL=ghrm
USER_ID=ghrm@pve
TOKEN_NAME=ghrm
ROOTFS_STORAGE=local-lvm
TEMPLATE_STORAGE=ghrm-tpl
TEMPLATE_DIR=/var/lib/ghrm-templates
OS_STORAGE=local         # where the Debian and Ubuntu container images are downloaded
LAN_BRIDGE=vmbr0
LAN_IP=dhcp              # dhcp, or an address with prefix (192.0.2.20/24)
LAN_GW=""
ZONE=runners
VNET=jobnet
SUBNET=10.50.0.0/24
SECURITY_GROUP=ghrm-job
DNS=1.1.1.1
ENV_RANGE=900-948
TEMPLATE_RANGE=950-958
CACHE_VMID=""            # registry cache; default: the existing one, else the next free ID
CACHE_DISK_GB=100
CACHE_DISK_SET=0         # 1 when --cache-disk-gb was given (an existing disk is otherwise kept as is)
DOCKERHUB_CLEAR=0
NO_CACHE=0
DOCKERHUB_USER=""        # optional; its token is read from standard input
REGISTRY_VERSION=3.1.2   # CNCF Distribution, checked against its published SHA-256
REGISTRY_SHA256=${GHRM_REGISTRY_SHA256:-40df2224d410f72ae425c3371873b078bbdbda3b8b612be9571f0e6751f3acc8}
VERSION=latest
BINARY_DIR=""            # use ghrm and ghrm-agent from this directory instead of a release
DRY_RUN=0

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
  cat <<EOF
Options (defaults in brackets):
  --node NAME              Proxmox node [$NODE]
  --vmid ID                control-plane container ID [existing, else next free]
  --template-vmid ID       bootstrap template ID [existing, else next free]
  --pool NAME              resource pool for ghrm's guests [$POOL]
  --rootfs-storage ID      storage for containers and clones (LVM-thin or ZFS) [$ROOTFS_STORAGE]
  --template-storage ID    dedicated storage for template archives [$TEMPLATE_STORAGE]
  --os-storage ID          storage for downloaded container images [$OS_STORAGE]
  --lan-bridge NAME        bridge of the LAN interface [$LAN_BRIDGE]
  --lan-ip CIDR|dhcp       control-plane LAN address [$LAN_IP]
  --lan-gw IP              LAN gateway (with a static --lan-ip)
  --zone NAME              SDN zone of the job network [$ZONE]
  --vnet NAME              SDN VNet of the job network [$VNET]
  --subnet CIDR            job network (/24) [$SUBNET]
  --security-group NAME    firewall group for job environments [$SECURITY_GROUP]
  --dns IP                 resolver for job environments [$DNS]
  --env-range A-B          VMIDs for job environments [$ENV_RANGE]
  --template-range A-B     VMIDs for built templates [$TEMPLATE_RANGE]
  --cache-vmid ID          registry cache container ID [existing, else next free]
  --cache-disk-gb N        registry cache disk [$CACHE_DISK_GB; an existing disk only grows]
  --no-cache               do not set up the registry cache
  --dockerhub-user NAME    Docker Hub account for the cache (raises the pull limit;
                           the access token is read from standard input). Every job
                           can pull what the account can: use a token with the
                           "Public Repo Read-only" scope
  --dockerhub-clear        remove the Docker Hub account from the cache
  --version vX.Y.Z|latest  ghrm release [$VERSION]
  --binary-dir DIR         install ghrm and ghrm-agent from DIR (development)
  --dry-run                show what would change, change nothing
EOF
}

while [ $# -gt 0 ]; do
  case $1 in
    --node) NODE=$2; shift ;;
    --vmid) CT_VMID=$2; shift ;;
    --template-vmid) TEMPLATE_VMID=$2; shift ;;
    --pool) POOL=$2; shift ;;
    --rootfs-storage) ROOTFS_STORAGE=$2; shift ;;
    --template-storage) TEMPLATE_STORAGE=$2; shift ;;
    --os-storage) OS_STORAGE=$2; shift ;;
    --lan-bridge) LAN_BRIDGE=$2; shift ;;
    --lan-ip) LAN_IP=$2; shift ;;
    --lan-gw) LAN_GW=$2; shift ;;
    --zone) ZONE=$2; shift ;;
    --vnet) VNET=$2; shift ;;
    --subnet) SUBNET=$2; shift ;;
    --security-group) SECURITY_GROUP=$2; shift ;;
    --dns) DNS=$2; shift ;;
    --env-range) ENV_RANGE=$2; shift ;;
    --template-range) TEMPLATE_RANGE=$2; shift ;;
    --cache-vmid) CACHE_VMID=$2; shift ;;
    --cache-disk-gb) CACHE_DISK_GB=$2; CACHE_DISK_SET=1; shift ;;
    --no-cache) NO_CACHE=1 ;;
    --dockerhub-user) DOCKERHUB_USER=$2; shift ;;
    --dockerhub-clear) DOCKERHUB_CLEAR=1 ;;
    --version) VERSION=$2; shift ;;
    --binary-dir) BINARY_DIR=$2; shift ;;
    --dry-run) DRY_RUN=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done

# The job network: gateway .1, the control plane's ingest address .2, DHCP .100-.199.
NET_PREFIX=${SUBNET%.*}
case $SUBNET in
  */24) ;;
  *) echo "--subnet must be a /24 (got $SUBNET)" >&2; exit 2 ;;
esac
GATEWAY=$NET_PREFIX.1
INGEST_IP=$NET_PREFIX.2
DHCP_START=$NET_PREFIX.100
DHCP_END=$NET_PREFIX.199
INGEST_PORT=8443
CACHE_IP=$NET_PREFIX.3
CACHE_ORIGINS="docker.io=https://registry-1.docker.io ghcr.io=https://ghcr.io mcr.microsoft.com=https://mcr.microsoft.com quay.io=https://quay.io"

# The Docker Hub token never touches the host's disk: it goes from standard input
# straight into the cache container's configuration.
DOCKERHUB_TOKEN=""
if [ -n "$DOCKERHUB_USER" ]; then
  if [ -t 0 ]; then
    read -r -s -p "Docker Hub access token for $DOCKERHUB_USER: " DOCKERHUB_TOKEN
    echo
  else
    read -r DOCKERHUB_TOKEN || true
  fi
  [ -n "$DOCKERHUB_TOKEN" ] || { echo "--dockerhub-user needs an access token on standard input" >&2; exit 2; }
fi

# --- output and dry run ----------------------------------------------------------------
CHANGED=0
if [ -t 1 ]; then GREEN=$'\033[32m' YELLOW=$'\033[33m' RED=$'\033[31m' RESET=$'\033[0m'; else GREEN='' YELLOW='' RED='' RESET=''; fi
exists() { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
created() { printf '  %s+%s %s\n' "$YELLOW" "$RESET" "$*"; CHANGED=1; }
note() { printf '    %s\n' "$*"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$*"; }
step() { printf '\n%s\n' "$*"; }
die() { printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

# run executes a command that changes something; with --dry-run it only prints it.
run() {
  if [ "$DRY_RUN" = 1 ]; then
    local shown=$*
    printf '    would run: %s\n' "${shown#quiet }" >&2
    return 0
  fi
  "$@"
}

# quiet CMD...: runs CMD and shows what it printed only when it fails.
quiet() {
  local out rc=0
  out=$("$@" 2>&1) || rc=$?
  if [ "$rc" != 0 ]; then printf '%s\n' "$out" >&2; fi
  return "$rc"
}

json() { pvesh get "$1" --output-format json; }

# has_entry PATH KEY VALUE: the JSON list at PATH has an entry whose KEY is VALUE.
# A PATH that does not exist yet (a user not created) has no entries.
has_entry() {
  local data
  data=$(json "$1" 2>/dev/null) || return 1
  printf '%s' "$data" | perl -MJSON::PP -0777 -e '
    my ($k, $v) = @ARGV; my $d = decode_json(<STDIN>);
    exit((grep { defined $_->{$k} && $_->{$k} eq $v } @$d) ? 0 : 1)' "$2" "$3"
}

# field PATH KEY prints a key of the JSON object at PATH.
field() { json "$1" | perl -MJSON::PP -0777 -e 'my $d = decode_json(<STDIN>); print $d->{$ARGV[0]} // ""' "$2"; }

# tagged TAG prints the VMID of the first container carrying TAG (exactly).
tagged() {
  json /cluster/resources | perl -MJSON::PP -0777 -e '
    my $t = $ARGV[0]; my $d = decode_json(<STDIN>);
    for (@$d) { next unless ($_->{type} // "") eq "lxc"; my %tags = map { $_ => 1 } split /[;,]/, ($_->{tags} // "");
      if ($tags{$t}) { print $_->{vmid}; last } }' "$1"
}

# --- 1. checks -------------------------------------------------------------------------
preflight() {
  step "Checking the host"
  [ "$(id -u)" = 0 ] || die "run as root on the Proxmox host"
  local v major minor
  v=$(pveversion | sed -n 's|^pve-manager/\([0-9][0-9]*\.[0-9][0-9]*\).*|\1|p')
  [ -n "$v" ] || die "pveversion did not report a Proxmox VE version"
  major=${v%.*} minor=${v#*.}
  if [ "$major" -lt 9 ] || { [ "$major" -eq 9 ] && [ "$minor" -lt 1 ]; }; then
    die "Proxmox VE 9.1 or later is required (found $v): ghrm passes the runner bootstrap through the LXC env option"
  fi
  local arch
  arch=$(dpkg --print-architecture)
  [ "$arch" = amd64 ] || die "ghrm ships amd64 binaries only (this host is $arch)"
  exists "Proxmox VE $v on node $NODE"
  if [ "$DRY_RUN" = 1 ]; then note "dry run: nothing will be changed"; fi
}

# --- 2. user, roles, pool, token, permissions -------------------------------------------
RUNTIME_PRIVS="VM.Allocate VM.Clone VM.Audit VM.PowerMgmt VM.Config.CPU VM.Config.Memory VM.Config.Disk VM.Config.Network VM.Config.Options Datastore.AllocateSpace Datastore.Audit SDN.Use Sys.Audit Pool.Audit"
TEMPLATE_PRIVS="Datastore.AllocateTemplate Datastore.Allocate Datastore.Audit"
TOKEN_SECRET=""

access() {
  step "Proxmox user and permissions"
  if has_entry /pools poolid "$POOL"; then exists "pool $POOL"; else run pveum pool add "$POOL" --comment "gh-runners-manager environments"; created "pool $POOL"; fi
  if has_entry /access/roles roleid GhrmRuntime; then exists "role GhrmRuntime"; else run pveum role add GhrmRuntime --privs "$RUNTIME_PRIVS"; created "role GhrmRuntime"; fi
  if has_entry /access/roles roleid GhrmTemplates; then exists "role GhrmTemplates"; else run pveum role add GhrmTemplates --privs "$TEMPLATE_PRIVS"; created "role GhrmTemplates"; fi
  if has_entry /access/users userid "$USER_ID"; then exists "user $USER_ID"; else run pveum user add "$USER_ID" --comment "gh-runners-manager"; created "user $USER_ID"; fi
  # ACLs are set every time (setting an existing one changes nothing).
  local path
  for path in "/pool/$POOL" "/storage/$ROOTFS_STORAGE" "/sdn/zones/$ZONE" "/nodes/$NODE"; do
    run pveum acl modify "$path" --users "$USER_ID" --roles GhrmRuntime
  done
  run pveum acl modify "/storage/$TEMPLATE_STORAGE" --users "$USER_ID" --roles GhrmTemplates
  exists "permissions on /pool/$POOL, /storage/$ROOTFS_STORAGE, /sdn/zones/$ZONE, /nodes/$NODE, /storage/$TEMPLATE_STORAGE"
}

# token makes sure the API token exists and its secret reaches the control plane. The
# secret is shown once, when the token is created, so a token whose secret the control
# plane does not have is created again.
token() {
  local have_secret=0 list
  if [ -n "$CT_VMID" ] && pct status "$CT_VMID" >/dev/null 2>&1; then
    if ! pct status "$CT_VMID" | grep -q running; then
      if [ "$DRY_RUN" = 1 ]; then
        note "container $CT_VMID is stopped: it would be started to check its secrets"
        have_secret=1
      else
        pct start "$CT_VMID"
        wait_for_container "$CT_VMID"
        created "started container $CT_VMID"
      fi
    fi
    if [ "$have_secret" = 0 ] && pct exec "$CT_VMID" -- test -x /usr/local/bin/ghrm; then
      # ghrm is installed: its answer decides, and a failure to read it stops here.
      list=$(pct exec "$CT_VMID" -- /usr/local/bin/ghrm secret list --config /etc/ghrm/ghrm.yaml) ||
        die "could not read the secrets of container $CT_VMID; fix ghrm there and run this again"
      if printf '%s\n' "$list" | grep -qx 'proxmox/token-secret' ||
        pct exec "$CT_VMID" -- sh -c 'f=$(sed -n "s/^ *token_secret_file: *\([^ #]*\).*/\1/p" /etc/ghrm/ghrm.yaml); [ -n "$f" ] && [ -s "$f" ]' 2>/dev/null; then
        have_secret=1
      fi
    fi
  fi
  if has_entry "/access/users/$USER_ID/token" tokenid "$TOKEN_NAME"; then
    if [ "$have_secret" = 1 ]; then exists "API token $USER_ID!$TOKEN_NAME"; return; fi
    run pveum user token remove "$USER_ID" "$TOKEN_NAME"
    note "the control plane does not have this token's secret: creating the token again"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    run pveum user token add "$USER_ID" "$TOKEN_NAME" --privsep 0
  else
    TOKEN_SECRET=$(pveum user token add "$USER_ID" "$TOKEN_NAME" --privsep 0 --output-format json | sed -n 's/.*"value":"\([^"]*\)".*/\1/p')
    [ -n "$TOKEN_SECRET" ] || die "could not read the new token's secret"
  fi
  created "API token $USER_ID!$TOKEN_NAME (its secret goes to the control plane's vault)"
}

# --- 3. storage ------------------------------------------------------------------------
storage() {
  step "Template storage"
  if has_entry /storage storage "$TEMPLATE_STORAGE"; then
    exists "storage $TEMPLATE_STORAGE"
  else
    run mkdir -p "$TEMPLATE_DIR"
    run pvesm add dir "$TEMPLATE_STORAGE" --path "$TEMPLATE_DIR" --content vztmpl
    created "storage $TEMPLATE_STORAGE ($TEMPLATE_DIR, container templates only)"
  fi
}

# --- 4. job network --------------------------------------------------------------------
network() {
  step "Job network"
  local sdn_changed=0
  if dpkg -s dnsmasq >/dev/null 2>&1; then
    exists "dnsmasq (SDN DHCP)"
  else
    run apt-get install -y dnsmasq
    run systemctl disable --now dnsmasq
    created "dnsmasq (SDN DHCP; the system instance stays disabled)"
  fi
  if has_entry /cluster/sdn/zones zone "$ZONE"; then exists "SDN zone $ZONE"; else
    run pvesh create /cluster/sdn/zones --zone "$ZONE" --type simple --dhcp dnsmasq --ipam pve
    created "SDN zone $ZONE (simple, DHCP)"; sdn_changed=1
  fi
  if has_entry /cluster/sdn/vnets vnet "$VNET"; then exists "VNet $VNET"; else
    run pvesh create /cluster/sdn/vnets --vnet "$VNET" --zone "$ZONE"
    created "VNet $VNET"; sdn_changed=1
  fi
  if [ "$sdn_changed" = 0 ] && has_entry "/cluster/sdn/vnets/$VNET/subnets" cidr "$SUBNET"; then exists "subnet $SUBNET"; else
    run pvesh create "/cluster/sdn/vnets/$VNET/subnets" --subnet "$SUBNET" --type subnet --gateway "$GATEWAY" --snat 1 \
      --dhcp-range "start-address=$DHCP_START,end-address=$DHCP_END" --dhcp-dns-server "$DNS"
    created "subnet $SUBNET (gateway $GATEWAY, SNAT, DHCP $DHCP_START-$DHCP_END, DNS $DNS)"; sdn_changed=1
  fi
  if [ "$sdn_changed" = 1 ]; then run quiet pvesh set /cluster/sdn; created "applied the SDN configuration"; fi
}

firewall() {
  step "Firewall"
  if [ "$(field /cluster/firewall/options enable)" = 1 ]; then
    exists "datacenter firewall enabled"
  else
    # Accepting by default keeps the host and every other guest as they were.
    run pvesh set /cluster/firewall/options --enable 1 --policy_in ACCEPT --policy_out ACCEPT
    created "datacenter firewall enabled (default policies ACCEPT: nothing else changes)"
  fi
  if has_entry /cluster/firewall/groups group "$SECURITY_GROUP"; then
    exists "security group $SECURITY_GROUP"
    if ! has_entry "/cluster/firewall/groups/$SECURITY_GROUP" dest "$INGEST_IP"; then
      note "warning: $SECURITY_GROUP has no rule for the ingest ($INGEST_IP:$INGEST_PORT); add it first in the group:"
      note "  pvesh create /cluster/firewall/groups/$SECURITY_GROUP --type out --action ACCEPT --proto tcp --dest $INGEST_IP --dport $INGEST_PORT --pos 0"
    fi
    # Additive and needed for the cache to work: added once, above the drops.
    if [ "$NO_CACHE" = 0 ] && ! has_entry "/cluster/firewall/groups/$SECURITY_GROUP" dest "$CACHE_IP"; then
      run pvesh create "/cluster/firewall/groups/$SECURITY_GROUP" --type out --action ACCEPT --proto tcp --dest "$CACHE_IP" --dport 5000:5003 \
        --enable 1 --comment "ghrm registry cache" --pos 0
      created "cache rule in security group $SECURITY_GROUP (jobs may reach $CACHE_IP:5000-5003)"
    fi
    return
  fi
  run pvesh create /cluster/firewall/groups --group "$SECURITY_GROUP" --comment "gh-runners-manager job environments"
  # A new rule goes first: create them last to first (spec §10.1).
  local g=/cluster/firewall/groups/$SECURITY_GROUP net
  for net in 169.254.0.0/16 192.168.0.0/16 172.16.0.0/12 10.0.0.0/8; do
    run pvesh create "$g" --type out --action DROP --dest "$net" --enable 1 --comment "no LAN or link-local"
  done
  run pvesh create "$g" --type in --action DROP --enable 1 --comment "nothing reaches a job"
  run pvesh create "$g" --type out --action ACCEPT --proto udp --dport 67 --enable 1 --comment "DHCP"
  if [ "$NO_CACHE" = 0 ]; then
    run pvesh create "$g" --type out --action ACCEPT --proto tcp --dest "$CACHE_IP" --dport 5000:5003 --enable 1 --comment "ghrm registry cache"
  fi
  run pvesh create "$g" --type out --action ACCEPT --proto tcp --dest "$INGEST_IP" --dport "$INGEST_PORT" --enable 1 --comment "ghrm ingest"
  created "security group $SECURITY_GROUP (ingest $INGEST_IP:$INGEST_PORT, DHCP, then no inbound and no private ranges)"
}

# --- 5. container images ---------------------------------------------------------------
# os_image PATTERN prints the volume ID of a downloaded image, downloading it if needed.
os_image() {
  local pattern=$1 name
  name=$(pveam list "$OS_STORAGE" | awk '{print $1}' | grep -E "$pattern" | grep '_amd64\.' | sort | tail -n 1 || true)
  if [ -n "$name" ]; then echo "$name"; return; fi
  run pveam update >/dev/null
  name=$(pveam available --section system | awk '{print $2}' | grep -E "${pattern#*/}" | grep '_amd64\.' | sort | tail -n 1)
  [ -n "$name" ] || die "no container image matches $pattern"
  run pveam download "$OS_STORAGE" "$name" >/dev/null
  echo "$OS_STORAGE:vztmpl/$name"
}

# --- 6. control plane ------------------------------------------------------------------
# next_free N prints the first VMID from N that no guest uses.
next_free() {
  local n=$1
  while pct status "$n" >/dev/null 2>&1; do n=$((n + 1)); done
  echo "$n"
}

# next_outside N: like next_free, skipping the control plane and ghrm's job and template
# ranges (ghrm counts on every ID there).
next_outside() {
  local n=$1 r
  while :; do
    n=$(next_free "$n")
    for r in "$ENV_RANGE" "$TEMPLATE_RANGE"; do
      if [ "$n" -ge "${r%-*}" ] && [ "$n" -le "${r#*-}" ]; then n=$((${r#*-} + 1)) && continue 2; fi
    done
    if [ "$n" = "$CT_VMID" ]; then n=$((n + 1)) && continue; fi
    echo "$n"
    return
  done
}

pick_vmids() {
  if [ -z "$CT_VMID" ]; then CT_VMID=$(tagged ghrm-control-plane); fi
  if [ -z "$CT_VMID" ]; then CT_VMID=$(next_free "$(pvesh get /cluster/nextid)"); fi
  # The template the control plane is configured with wins: a run that failed half-way
  # resumes with it.
  if [ -z "$TEMPLATE_VMID" ] && pct status "$CT_VMID" 2>/dev/null | grep -q running; then
    TEMPLATE_VMID=$(pct exec "$CT_VMID" -- sed -n 's/^ *template_vmid: *\([0-9]*\).*/\1/p' /etc/ghrm/ghrm.yaml 2>/dev/null || true)
  fi
  if [ -z "$TEMPLATE_VMID" ]; then TEMPLATE_VMID=$(tagged ghrm-template); fi
  if [ -z "$TEMPLATE_VMID" ]; then TEMPLATE_VMID=$(tagged ghrm-template-building); fi
  if [ -z "$TEMPLATE_VMID" ]; then TEMPLATE_VMID=$(next_free $((CT_VMID + 1))); fi
  if [ -z "$CACHE_VMID" ]; then CACHE_VMID=$(tagged ghrm-cache); fi
  if [ -z "$CACHE_VMID" ]; then CACHE_VMID=$(next_outside $((TEMPLATE_VMID + 1))); fi
}

host_ip() { ip -4 route get 1.1.1.1 | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n 1; }

fingerprint() {
  local pem=/etc/pve/local/pveproxy-ssl.pem
  [ -f "$pem" ] || pem=/etc/pve/local/pve-ssl.pem
  openssl x509 -in "$pem" -noout -fingerprint -sha256 | cut -d= -f2
}

control_plane() {
  step "Control plane (container $CT_VMID)"
  if pct status "$CT_VMID" >/dev/null 2>&1; then
    exists "container $CT_VMID"
    return
  fi
  local image net0
  image=$(os_image 'debian-13-standard')
  net0="name=eth0,bridge=$LAN_BRIDGE,ip=$LAN_IP"
  if [ -n "$LAN_GW" ]; then net0="$net0,gw=$LAN_GW"; fi
  run pct create "$CT_VMID" "$image" --hostname ghrm --unprivileged 1 --features nesting=1 --cores 1 --memory 2048 --swap 512 \
    --rootfs "$ROOTFS_STORAGE:16" --net0 "$net0" --net1 "name=eth1,bridge=$VNET,ip=$INGEST_IP/24" \
    --onboot 1 --tags ghrm-control-plane --description "gh-runners-manager control plane" >/dev/null
  run pct start "$CT_VMID"
  created "container $CT_VMID (Debian 13, 1 vCPU, 2 GiB; LAN on $LAN_BRIDGE, ingest $INGEST_IP on $VNET)"
}

# binaries puts ghrm and ghrm-agent (checksums verified) in $1.
binaries() {
  local dir=$1 base
  if [ -n "$BINARY_DIR" ]; then
    cp "$BINARY_DIR/ghrm" "$BINARY_DIR/ghrm-agent" "$dir/"
    return
  fi
  if [ "$VERSION" = latest ]; then base="https://github.com/$REPO/releases/latest/download"; else base="https://github.com/$REPO/releases/download/$VERSION"; fi
  curl -fsSL -o "$dir/ghrm" "$base/ghrm-linux-amd64"
  curl -fsSL -o "$dir/ghrm-agent" "$base/ghrm-agent-linux-amd64"
  curl -fsSL -o "$dir/SHA256SUMS" "$base/SHA256SUMS"
  # SHA256SUMS also lists install.sh: check the two binaries, and that both are listed.
  local sums
  sums=$(grep -E '^[0-9a-f]{64}  (ghrm|ghrm-agent)-linux-amd64$' "$dir/SHA256SUMS" |
    sed -e 's/ghrm-agent-linux-amd64$/ghrm-agent/' -e 's/ghrm-linux-amd64$/ghrm/' || true)
  [ "$(printf '%s\n' "$sums" | grep -c .)" = 2 ] || die "SHA256SUMS does not list both binaries"
  (cd "$dir" && printf '%s\n' "$sums" | sha256sum -c --quiet -) || die "the downloaded binaries do not match SHA256SUMS"
  chmod +x "$dir/ghrm" "$dir/ghrm-agent"
}

config_file() {
  local api_ip=$1 fp=$2 env_start=${ENV_RANGE%-*} env_end=${ENV_RANGE#*-} tpl_start=${TEMPLATE_RANGE%-*} tpl_end=${TEMPLATE_RANGE#*-}
  cat <<EOF
# Written by install.sh. GitHub credentials and scale sets are added in the web UI.
data_dir: /var/lib/ghrm
# The job security group only lets jobs reach the ingest port, so listening on every
# interface does not expose the UI to jobs.
listen: 0.0.0.0:8080
admin_token_file: /etc/ghrm/admin-token   # API token for scripts (Bearer)

proxmox:
  url: https://$api_ip:8006
  node: $NODE
  token_id: $USER_ID!$TOKEN_NAME          # its secret is in the vault (ghrm secret list)
  tls_fingerprint: "$fp"
  template_vmid: $TEMPLATE_VMID
  pool: $POOL
  vmid_range: {start: $env_start, end: $env_end}
  storage: $ROOTFS_STORAGE

ingest:
  listen: $INGEST_IP:$INGEST_PORT
  advertise_url: https://$INGEST_IP:$INGEST_PORT

templates:
  vmid_range: {start: $tpl_start, end: $tpl_end}
  storage: $TEMPLATE_STORAGE
  nameserver: $DNS
  bridge: $VNET
  firewall_group: $SECURITY_GROUP
  selftest_blocked: ["$api_ip:8006", "$api_ip:22"]
EOF
  if [ "$NO_CACHE" = 0 ]; then cache_section; fi
}

GHRM_SERVICE='[Unit]
Description=gh-runners-manager control plane
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/ghrm serve --config /etc/ghrm/ghrm.yaml
Restart=on-failure
RestartSec=5
# Keep live environments on stop: they are adopted at the next start.
KillMode=mixed
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target'

wait_for_container() {
  local vmid=$1 _
  for _ in $(seq 1 60); do
    if pct exec "$vmid" -- sh -c 'ip -4 route | grep -q default' >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  die "container $vmid has no network after 60 s"
}

install_ghrm() {
  step "ghrm in container $CT_VMID"
  if [ "$DRY_RUN" = 1 ]; then
    note "would install ghrm and ghrm-agent ($VERSION), write /etc/ghrm/ghrm.yaml if missing, store the token secret, and (re)start the service"
    return
  fi
  wait_for_container "$CT_VMID"
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  binaries "$tmp"
  local want have restart=0
  want=$("$tmp/ghrm" version | awk '{print $2}')
  have=$(pct exec "$CT_VMID" -- /usr/local/bin/ghrm version 2>/dev/null | awk '{print $2}' || true)
  if [ -n "$want" ] && [ "$want" = "$have" ]; then
    exists "ghrm $want (current)"
  else
    pct exec "$CT_VMID" -- env LC_ALL=C DEBIAN_FRONTEND=noninteractive sh -c 'command -v curl >/dev/null || (apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null 2>&1)'
    pct exec "$CT_VMID" -- mkdir -p /etc/ghrm /var/lib/ghrm
    pct exec "$CT_VMID" -- sh -c 'systemctl stop ghrm 2>/dev/null || true'
    pct push "$CT_VMID" "$tmp/ghrm" /usr/local/bin/ghrm --perms 0755
    pct push "$CT_VMID" "$tmp/ghrm-agent" /usr/local/bin/ghrm-agent --perms 0755
    created "ghrm $want and ghrm-agent${have:+ (was $have)}"
    restart=1
  fi
  if pct exec "$CT_VMID" -- test -f /etc/ghrm/ghrm.yaml; then
    exists "/etc/ghrm/ghrm.yaml (kept)"
    if [ "$NO_CACHE" = 0 ] && ! pct exec "$CT_VMID" -- grep -q ^cache: /etc/ghrm/ghrm.yaml; then
      cache_section | pct exec "$CT_VMID" -- sh -c "cat >> /etc/ghrm/ghrm.yaml"
      created "cache settings added to /etc/ghrm/ghrm.yaml"
      restart=1
    fi
  else
    config_file "$(host_ip)" "$(fingerprint)" >"$tmp/ghrm.yaml"
    pct push "$CT_VMID" "$tmp/ghrm.yaml" /etc/ghrm/ghrm.yaml --perms 0640
    created "/etc/ghrm/ghrm.yaml"
    restart=1
  fi
  if pct exec "$CT_VMID" -- test -s /etc/ghrm/admin-token; then exists "API admin token (kept)"; else
    openssl rand -hex 32 >"$tmp/admin-token"
    pct push "$CT_VMID" "$tmp/admin-token" /etc/ghrm/admin-token --perms 0600
    created "API admin token in /etc/ghrm/admin-token"
    restart=1
  fi
  if [ -n "$TOKEN_SECRET" ]; then
    printf '%s\n' "$TOKEN_SECRET" | pct exec "$CT_VMID" -- /usr/local/bin/ghrm secret set --config /etc/ghrm/ghrm.yaml proxmox/token-secret
    created "Proxmox token secret sealed in the control plane's vault"
    restart=1
  fi
  printf '%s\n' "$GHRM_SERVICE" >"$tmp/ghrm.service"
  pct push "$CT_VMID" "$tmp/ghrm.service" /etc/systemd/system/ghrm.service --perms 0644
  pct exec "$CT_VMID" -- systemctl daemon-reload
  pct exec "$CT_VMID" -- systemctl enable --now ghrm >/dev/null 2>&1
  if [ "$restart" = 1 ]; then pct exec "$CT_VMID" -- systemctl restart ghrm; fi
  exists "service ghrm running"
}

# --- 6b. registry cache ----------------------------------------------------------------
cache_section() {
  cat <<EOF

# Pull-through registry cache on the job network (written by install.sh).
cache:
  address: $CACHE_IP
EOF
}

registry_config() { # registry_config ORIGIN UPSTREAM PORT METRICS_PORT
  cat <<EOF
# Written by install.sh: a pull-through cache of $1 for gh-runners-manager jobs.
version: 0.1
log:
  level: info
storage:
  filesystem:
    rootdirectory: /var/lib/ghrm-cache/$1
  delete:
    enabled: true
http:
  addr: ":$3"
  debug:
    addr: ":$4"
    prometheus:
      enabled: true
      path: /metrics
proxy:
  remoteurl: $2
  ttl: 168h
EOF
  if [ "$1" = docker.io ] && [ -n "$DOCKERHUB_USER" ]; then
    printf '  username: %s\n  password: %s\n' "$DOCKERHUB_USER" "$DOCKERHUB_TOKEN"
  fi
}

# put_file VMID PATH [MODE]: writes standard input to PATH in the container (0600, or
# MODE, applied even when the content is unchanged) when it differs; it returns 0 when
# it changed the content.
put_file() {
  local vmid=$1 path=$2 mode=${3:-} want have
  want=$(cat)
  have=$(pct exec "$vmid" -- cat "$path" 2>/dev/null || true)
  if [ "$want" = "$have" ]; then
    [ -z "$mode" ] || pct exec "$vmid" -- chmod "$mode" "$path"
    return 1
  fi
  printf '%s\n' "$want" | pct exec "$vmid" -- sh -c "umask 077; cat > $path"
  [ -z "$mode" ] || pct exec "$vmid" -- chmod "$mode" "$path"
  return 0
}

# push_binary VMID SRC DEST: replaces DEST even while it runs. pct push cannot write a
# busy executable (and still exits 0); a rename next to it can.
push_binary() {
  pct push "$1" "$2" "$3.new" --perms 0755
  pct exec "$1" -- mv -f "$3.new" "$3"
}

cache() {
  step "Registry cache (container $CACHE_VMID)"
  if [ "$NO_CACHE" = 1 ]; then
    note "skipped (--no-cache)"
    return
  fi
  if pct status "$CACHE_VMID" >/dev/null 2>&1; then
    exists "container $CACHE_VMID (registry cache)"
    if ! pct status "$CACHE_VMID" | grep -q running; then
      run pct start "$CACHE_VMID"
      created "started container $CACHE_VMID"
    fi
    # The disk the container has sets the budget; --cache-disk-gb can only grow it.
    local size
    size=$(pct config "$CACHE_VMID" | sed -n 's/^rootfs:.*size=\([0-9]*\)G.*/\1/p')
    if [ -n "$size" ]; then
      if [ "$CACHE_DISK_SET" = 1 ] && [ "$CACHE_DISK_GB" -gt "$size" ]; then
        run pct resize "$CACHE_VMID" rootfs "${CACHE_DISK_GB}G"
        created "cache disk grown from $size GB to $CACHE_DISK_GB GB"
      else
        if [ "$CACHE_DISK_SET" = 1 ] && [ "$CACHE_DISK_GB" -lt "$size" ]; then
          warn "the cache disk is $size GB and Proxmox cannot shrink it: keeping $size GB"
        fi
        CACHE_DISK_GB=$size
      fi
    fi
  else
    local image
    image=$(os_image 'debian-13-standard')
    run pct create "$CACHE_VMID" "$image" --hostname ghrm-cache --unprivileged 1 --features nesting=1 --cores 1 --memory 512 --swap 0 \
      --rootfs "$ROOTFS_STORAGE:$CACHE_DISK_GB,mountoptions=discard" --net0 "name=eth0,bridge=$VNET,ip=$CACHE_IP/24,gw=$GATEWAY" --nameserver "$DNS" \
      --onboot 1 --tags ghrm-cache --description "gh-runners-manager registry cache" >/dev/null
    run pct start "$CACHE_VMID"
    created "container $CACHE_VMID (registry cache on $CACHE_IP, $CACHE_DISK_GB GB)"
  fi
  if [ "$DRY_RUN" = 1 ]; then
    if pct status "$CACHE_VMID" >/dev/null 2>&1; then
      note "would check the registry v$REGISTRY_VERSION proxies, eviction timer and disk exporter, and update what differs"
    else
      note "would install the registry v$REGISTRY_VERSION (one proxy per registry), its eviction timer and disk exporter"
    fi
    return
  fi
  wait_for_container "$CACHE_VMID"
  local tmp changed="" entry origin url port metrics i=0
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  # "registry github.com/distribution/distribution/v3 3.1.2": the last word is the version.
  if [ "$(pct exec "$CACHE_VMID" -- /usr/local/bin/registry --version 2>/dev/null | awk '{print $NF}')" != "$REGISTRY_VERSION" ]; then
    curl -fsSL -o "$tmp/registry.tar.gz" \
      "https://github.com/distribution/distribution/releases/download/v$REGISTRY_VERSION/registry_${REGISTRY_VERSION}_linux_amd64.tar.gz"
    echo "$REGISTRY_SHA256  $tmp/registry.tar.gz" | sha256sum -c --quiet - || die "the registry download does not match its SHA-256"
    tar -xzf "$tmp/registry.tar.gz" -C "$tmp" registry
    push_binary "$CACHE_VMID" "$tmp/registry" /usr/local/bin/registry
    created "registry v$REGISTRY_VERSION"
    changed="$changed registry"
  fi
  binaries "$tmp"
  if [ "$(sha256sum "$tmp/ghrm-agent" | cut -d' ' -f1)" != "$(pct exec "$CACHE_VMID" -- sha256sum /usr/local/bin/ghrm-agent 2>/dev/null | cut -d' ' -f1 || true)" ]; then
    push_binary "$CACHE_VMID" "$tmp/ghrm-agent" /usr/local/bin/ghrm-agent
    created "ghrm-agent (eviction and disk exporter)"
    changed="$changed agent"
  fi
  pct exec "$CACHE_VMID" -- mkdir -p /etc/ghrm-cache /var/lib/ghrm-cache
  local instances=""
  for entry in $CACHE_ORIGINS; do
    origin=${entry%%=*} url=${entry#*=} port=$((5000 + i)) metrics=$((5100 + i)) i=$((i + 1))
    instances="$instances --instance $origin=/etc/ghrm-cache/$origin.yml"
    # A credential set by an earlier run is kept (its token is only in the container),
    # unless --dockerhub-clear removes it.
    if [ "$origin" = docker.io ] && [ -z "$DOCKERHUB_USER" ] && [ "$DOCKERHUB_CLEAR" = 0 ]; then
      local current
      current=$(pct exec "$CACHE_VMID" -- cat /etc/ghrm-cache/docker.io.yml 2>/dev/null || true)
      DOCKERHUB_USER=$(printf '%s\n' "$current" | sed -n 's/^  username: //p')
      DOCKERHUB_TOKEN=$(printf '%s\n' "$current" | sed -n 's/^  password: //p')
      if [ -n "$DOCKERHUB_USER" ]; then exists "Docker Hub credential for $DOCKERHUB_USER (kept)"; fi
    fi
    if registry_config "$origin" "$url" "$port" "$metrics" | put_file "$CACHE_VMID" "/etc/ghrm-cache/$origin.yml"; then
      created "proxy for $origin on port $port"
      changed="$changed $origin"
    fi
  done
  local budget=$((CACHE_DISK_GB * 9 / 10))
  if put_file "$CACHE_VMID" /etc/systemd/system/ghrm-cache-registry@.service 0644 <<EOF; then changed="$changed units"; fi
[Unit]
Description=gh-runners-manager registry cache for %i
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/usr/local/bin/registry serve /etc/ghrm-cache/%i.yml
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
  if put_file "$CACHE_VMID" /etc/systemd/system/ghrm-cache-exporter.service 0644 <<EOF; then changed="$changed units"; fi
[Unit]
Description=gh-runners-manager registry cache disk exporter

[Service]
ExecStart=/usr/local/bin/ghrm-agent cache-exporter --listen :5199 --status /var/lib/ghrm-cache/status
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
  if put_file "$CACHE_VMID" /etc/systemd/system/ghrm-cache-prune.service 0644 <<EOF; then changed="$changed units"; fi
[Unit]
Description=gh-runners-manager registry cache eviction

[Service]
Type=oneshot
ExecStart=/usr/local/bin/ghrm-agent cache-prune --root /var/lib/ghrm-cache --budget-gb $budget$instances
EOF
  if put_file "$CACHE_VMID" /etc/systemd/system/ghrm-cache-prune.timer 0644 <<EOF; then changed="$changed units"; fi
[Unit]
Description=Keep the gh-runners-manager registry cache under its disk budget

[Timer]
OnBootSec=2min
OnUnitActiveSec=15min

[Install]
WantedBy=timers.target
EOF
  if [ -z "$changed" ]; then
    exists "registry v$REGISTRY_VERSION proxies, eviction timer and disk exporter"
    return
  fi
  local units="ghrm-cache-exporter.service ghrm-cache-prune.timer"
  for entry in $CACHE_ORIGINS; do units="$units ghrm-cache-registry@${entry%%=*}.service"; done
  pct exec "$CACHE_VMID" -- systemctl daemon-reload
  # shellcheck disable=SC2086 # one word per unit
  pct exec "$CACHE_VMID" -- systemctl enable --now $units >/dev/null 2>&1
  # shellcheck disable=SC2086
  pct exec "$CACHE_VMID" -- systemctl restart $units
  created "registry cache services (re)started"
}

# --- 7. bootstrap template -------------------------------------------------------------
GHRM_AGENT_SERVICE='[Unit]
Description=gh-runners-manager agent (runs one GitHub Actions job, then powers off)
Wants=network-online.target docker.service
After=network-online.target docker.service
ConditionPathExists=/home/runner/actions-runner/run.sh

[Service]
Type=simple
ExecStart=/usr/local/bin/ghrm-agent
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=multi-user.target'

# Run inside the bootstrap template: Docker, the GitHub runner (checksum verified) and
# the runner user, as the ghrm layer installs them on ubuntu-slim.
PROVISION='set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl git jq sudo gnupg >/dev/null
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
. /etc/os-release
echo "deb [signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable" >/etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
id runner >/dev/null 2>&1 || useradd -m -s /bin/bash -u 1001 runner
usermod -aG docker runner
echo "runner ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/runner && chmod 0440 /etc/sudoers.d/runner
release=$(curl -fsSL https://api.github.com/repos/actions/runner/releases/latest)
version=$(echo "$release" | jq -r .tag_name | sed "s/^v//")
sha=$(echo "$release" | jq -r .body | sed -n "s/.*<!-- BEGIN SHA linux-x64 -->\([0-9a-f]*\)<!-- END SHA linux-x64 -->.*/\1/p")
mkdir -p /home/runner/actions-runner && cd /home/runner/actions-runner
curl -fsSL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v$version/actions-runner-linux-x64-$version.tar.gz"
echo "$sha  runner.tar.gz" | sha256sum -c -
tar xzf runner.tar.gz && rm runner.tar.gz
./bin/installdependencies.sh >/dev/null
chown -R runner:runner /home/runner
sed -i "/^LANG=/d" /etc/environment && echo LANG=C.UTF-8 >>/etc/environment && echo LANG=C.UTF-8 >/etc/default/locale
systemctl daemon-reload
systemctl enable ghrm-agent.service docker.service
apt-get clean
truncate -s 0 /etc/machine-id
rm -f /var/lib/dbus/machine-id /etc/ssh/ssh_host_*'

bootstrap_template() {
  step "Bootstrap template (container $TEMPLATE_VMID)"
  if pct status "$TEMPLATE_VMID" >/dev/null 2>&1; then
    local conf
    conf=$(pct config "$TEMPLATE_VMID")
    if printf '%s\n' "$conf" | grep -qE '^tags:.*ghrm-template-building'; then
      note "container $TEMPLATE_VMID is an unfinished bootstrap template from an earlier run: building it again"
      run pct stop "$TEMPLATE_VMID" >/dev/null 2>&1 || true
      run pct destroy "$TEMPLATE_VMID" --purge
    elif printf '%s\n' "$conf" | grep -q '^template: 1'; then
      exists "template $TEMPLATE_VMID"
      return
    else
      die "VMID $TEMPLATE_VMID is not a ghrm template (a guest that is not one uses it); pick another with --template-vmid"
    fi
  fi
  local image
  image=$(os_image 'ubuntu-24.04-standard')
  run pct create "$TEMPLATE_VMID" "$image" --hostname ghrm-template --unprivileged 1 --features nesting=1 --cores 2 --memory 2048 \
    --rootfs "$ROOTFS_STORAGE:8" --net0 "name=eth0,bridge=$VNET,ip=dhcp,firewall=1" --nameserver "$DNS" --ostype ubuntu --pool "$POOL" \
    --tags ghrm-template-building >/dev/null
  run pvesh set "/nodes/$NODE/lxc/$TEMPLATE_VMID/firewall/options" --enable 1
  run pvesh create "/nodes/$NODE/lxc/$TEMPLATE_VMID/firewall/rules" --type group --action "$SECURITY_GROUP" --enable 1
  if [ "$DRY_RUN" = 1 ]; then
    note "would install Docker, the GitHub runner and ghrm-agent, then convert it to a template"
    created "template $TEMPLATE_VMID"
    return
  fi
  sleep "${GHRM_FIREWALL_SETTLE:-12}" # let pve-firewall apply the group before the container goes online
  pct start "$TEMPLATE_VMID"
  wait_for_container "$TEMPLATE_VMID"
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  binaries "$tmp"
  printf '%s\n' "$GHRM_AGENT_SERVICE" >"$tmp/ghrm-agent.service"
  pct push "$TEMPLATE_VMID" "$tmp/ghrm-agent" /usr/local/bin/ghrm-agent --perms 0755
  pct push "$TEMPLATE_VMID" "$tmp/ghrm-agent.service" /etc/systemd/system/ghrm-agent.service --perms 0644
  printf '%s\n' "$PROVISION" | quiet pct exec "$TEMPLATE_VMID" -- env LC_ALL=C bash -s
  pct shutdown "$TEMPLATE_VMID" --timeout 60
  quiet pct template "$TEMPLATE_VMID"
  pct set "$TEMPLATE_VMID" --tags ghrm-template
  created "template $TEMPLATE_VMID (Ubuntu 24.04, Docker, GitHub runner, ghrm-agent); ghrm builds the real templates from it"
}

# --- 8. summary ------------------------------------------------------------------------
summary() {
  step "Done"
  if [ "$DRY_RUN" = 1 ]; then
    if [ "$CHANGED" = 0 ]; then note "everything is in place"; else note "run without --dry-run to make the changes above"; fi
    return
  fi
  local ip tok
  ip=$(pct exec "$CT_VMID" -- sh -c "ip -4 -o addr show eth0 | awk '{print \$4}' | cut -d/ -f1" 2>/dev/null || true)
  note "web UI: http://${ip:-<control-plane address>}:8080"
  tok=$(pct exec "$CT_VMID" -- cat /var/lib/ghrm/setup-token 2>/dev/null || true)
  if [ -n "$tok" ]; then
    note "setup token: $tok"
    note "open the UI, create the admin account with this token, then add a GitHub credential"
    note "and a scale set, and build the first template (Templates > Build now)."
  fi
}

preflight
pick_vmids
access
token
storage
network
firewall
cache
# ghrm builds from the bootstrap template as soon as it starts: create it first.
bootstrap_template
control_plane
install_ghrm
summary
