#!/usr/bin/env bash
# Tests for install.sh against fake Proxmox tools (fakebin/fake-pve). Run: bash run.sh
set -u
here=$(cd "$(dirname "$0")" && pwd)
installer=${INSTALLER:-$here/../install.sh}
repo=$(cd "$here/../../.." && pwd)
failures=0 count=0

setup() {
  tmp=$(mktemp -d)
  export FAKE_STATE=$tmp/state FAKE_LOG=$tmp/log GHRM_FIREWALL_SETTLE=0
  GHRM_REGISTRY_SHA256=$(sha256sum "$here/fixtures/registry.tar.gz" | cut -d' ' -f1)
  export GHRM_REGISTRY_SHA256
  mkdir -p "$FAKE_STATE" "$tmp/bin"
  : >"$FAKE_LOG"
  binaries v1.0.0
}

binaries() { # binaries VERSION: the --binary-dir build
  printf '#!/bin/sh\necho "ghrm %s (abc)"\n' "$1" >"$tmp/bin/ghrm"
  printf '#!/bin/sh\necho "ghrm-agent %s (abc)"\n' "$1" >"$tmp/bin/ghrm-agent"
  chmod +x "$tmp/bin/ghrm" "$tmp/bin/ghrm-agent"
}

install() { # install ARGS...: runs the installer, output in $tmp/out
  PATH="$here/fakebin:$PATH" bash "$installer" --binary-dir "$tmp/bin" "$@" >"$tmp/out" 2>&1
}

install_release() { # like install, downloading the release
  PATH="$here/fakebin:$PATH" bash "$installer" >"$tmp/out" 2>&1
}

fail() { echo "  FAIL: $*"; failures=$((failures + 1)); }
expect_out() { grep -qF -- "$1" "$tmp/out" || fail "output lacks: $1"; }
expect_no_out() { ! grep -qE -- "$1" "$tmp/out" || fail "output has: $1 ($(grep -E -- "$1" "$tmp/out" | head -n 1))"; }
expect_log() { grep -qE -- "$1" "$FAKE_LOG" || fail "no call matching: $1"; }
expect_no_log() { ! grep -qE -- "$1" "$FAKE_LOG" || fail "unexpected call: $(grep -E -- "$1" "$FAKE_LOG" | head -n 1)"; }

CREATES='^(pveum (pool|role|user) add|pveum user token (add|remove)|pvesm add|pvesh create|pvesh set /cluster|pct (create|template)|pveam download|apt-get install|mkdir)'

test_install_creates_everything() {
  install || fail "exit $?: $(tail -n 3 "$tmp/out")"
  for want in "+ dnsmasq" "+ pool ghrm" "+ role GhrmRuntime" "+ role GhrmTemplates" "+ user ghrm@pve" "+ API token ghrm@pve!ghrm" \
    "+ storage ghrm-tpl" "+ SDN zone runners" "+ VNet jobnet" "+ subnet 10.50.0.0/24" "+ applied the SDN configuration" \
    "+ security group ghrm-job" "+ container 100" "+ /etc/ghrm/ghrm.yaml" "+ API admin token" "+ Proxmox token secret sealed" "+ template 101" \
    "setup token: setup-token-xyz" "web UI: http://192.0.2.20:8080"; do
    expect_out "$want"
  done
  expect_log 'pct exec 100 -- /usr/local/bin/ghrm secret set --config /etc/ghrm/ghrm.yaml proxmox/token-secret'
  # pct exec has no /usr/local/bin in its PATH: ghrm is always called by its full path.
  expect_no_log 'pct exec [0-9]+ -- ghrm '
  expect_log 'pct create 100 local:vztmpl/debian-13-standard.* --net1 name=eth1,bridge=jobnet,ip=10.50.0.2/24'
  expect_log 'pct create 101 local:vztmpl/ubuntu-24.04-standard.* --pool ghrm'
  expect_log 'pvesh create /nodes/pve/lxc/101/firewall/rules --type group --action ghrm-job'
  # Rules are inserted at the top: the ingest exception must be created last, so it comes first.
  last_rule=$(grep 'pvesh create /cluster/firewall/groups/ghrm-job' "$FAKE_LOG" | tail -n 1)
  [[ $last_rule == *"--dest 10.50.0.2 --dport 8443"* ]] || fail "the ingest rule must be created last, got: $last_rule"
  first_rule=$(grep 'pvesh create /cluster/firewall/groups/ghrm-job' "$FAKE_LOG" | head -n 1)
  [[ $first_rule == *"--action DROP --dest 169.254.0.0/16"* ]] || fail "the private-range drops go last, got: $first_rule"
  expect_log 'pvesh set /cluster/firewall/options --enable 1 --policy_in ACCEPT --policy_out ACCEPT'
}

test_install_is_idempotent() {
  install || fail "first run: exit $?"
  : >"$FAKE_LOG"
  install || fail "second run: exit $?: $(tail -n 3 "$tmp/out")"
  expect_no_log "$CREATES"
  expect_no_out '^  \+ (pool|role|user|API token|storage|SDN|VNet|subnet|security|container|template|/etc/ghrm|ghrm)'
  # The same ghrm is not reinstalled, and the service is not restarted.
  expect_no_log 'pct push [0-9]+ \S+ /usr/local/bin/ghrm '
  expect_no_log 'systemctl restart'
  expect_out "✓ ghrm v1.0.0 (current)"
  for want in "✓ pool ghrm" "✓ API token ghrm@pve!ghrm" "✓ security group ghrm-job" "✓ container 100" "✓ template 101" "✓ /etc/ghrm/ghrm.yaml (kept)"; do
    expect_out "$want"
  done
}

seed_dev_host() { # an existing hand-made setup, like the development host
  printf 'ghrm\n' >"$FAKE_STATE/pools"
  printf 'GhrmRuntime\nGhrmTemplates\n' >"$FAKE_STATE/roles"
  printf 'ghrm@pve\n' >"$FAKE_STATE/users"
  printf 'ghrm@pve ghrm\n' >"$FAKE_STATE/tokens"
  printf 'local\nlocal-lvm\nghrm-tpl\n' >"$FAKE_STATE/storages"
  printf 'runners\n' >"$FAKE_STATE/zones"
  printf 'jobnet\n' >"$FAKE_STATE/vnets"
  printf '10.50.0.0/24\n' >"$FAKE_STATE/subnets"
  printf 'gh-runner\n' >"$FAKE_STATE/groups"
  printf '10.50.0.2\n' >"$FAKE_STATE/group_gh-runner"
  printf '1\n' >"$FAKE_STATE/fw_enable"
  printf '310  running\n951 ghrm-template template\n' >"$FAKE_STATE/guests"
  printf '310 proxmox/token-secret\n' >"$FAKE_STATE/ct_secrets"
  printf '310 /etc/ghrm/ghrm.yaml\n310 /etc/ghrm/admin-token\n310 /usr/local/bin/ghrm\n' >"$FAKE_STATE/ct_files"
  echo "ghrm v1.0.0 (abc)" >"$FAKE_STATE/ct_version_310"
  echo "310 951" >"$FAKE_STATE/ct_tplvmid"
  touch "$FAKE_STATE/dnsmasq"
}

test_install_reuses_existing_setup() {
  seed_dev_host
  install --vmid 310 --security-group gh-runner --no-cache || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_no_log "$CREATES"
  expect_out "✓ template 951" # found by its tag
  expect_out "✓ container 310"
  expect_no_out "warning"
}

test_missing_ingest_rule_is_reported() {
  seed_dev_host
  : >"$FAKE_STATE/group_gh-runner"
  install --vmid 310 --security-group gh-runner --no-cache || fail "exit $?"
  expect_out "has no rule for the ingest (10.50.0.2:8443)"
  expect_no_log 'pvesh create /cluster/firewall'
}

test_token_without_its_secret_is_created_again() {
  seed_dev_host
  : >"$FAKE_STATE/ct_secrets"
  install --vmid 310 --security-group gh-runner || fail "exit $?"
  expect_log 'pveum user token remove ghrm@pve ghrm'
  expect_log 'pveum user token add ghrm@pve ghrm --privsep 0'
  expect_log 'pct exec 310 -- /usr/local/bin/ghrm secret set'
}

test_token_in_a_secret_file_is_kept() {
  seed_dev_host
  : >"$FAKE_STATE/ct_secrets"
  echo "310 token_secret_file" >"$FAKE_STATE/ct_config" # the config names a secret file that exists
  install --vmid 310 --security-group gh-runner || fail "exit $?"
  expect_no_log 'pveum user token (remove|add)'
  expect_out "✓ API token ghrm@pve!ghrm"
}

test_download_path_verifies_checksums() {
  install_release || fail "release download: exit $?: $(tail -n 3 "$tmp/out")"
  expect_out "+ ghrm v1.0.0"
  setup
  if FAKE_BAD_SUM=1 install_release; then fail "a checksum mismatch must stop the installer"; fi
  expect_out "do not match SHA256SUMS"
}

test_stopped_control_plane_is_started_not_replaced() {
  seed_dev_host
  sed -i.bak 's/^310  running$/310  stopped/' "$FAKE_STATE/guests"
  install --vmid 310 --security-group gh-runner || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_log '^pct start 310'
  expect_no_log 'pveum user token (remove|add)'
}

test_vmids_skip_guests_in_use() {
  printf '101  running\n' >"$FAKE_STATE/guests" # someone else's container
  install || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_out "+ container 100"
  expect_out "+ template 102"
  expect_no_log 'pct (create|exec|push|destroy|set|start) 101'
}

test_unfinished_template_is_rebuilt() {
  install || fail "first run: exit $?"
  sed -i.bak 's/^101 ghrm-template template$/101 ghrm-template-building running/' "$FAKE_STATE/guests"
  : >"$FAKE_LOG"
  install || fail "second run: exit $?: $(tail -n 3 "$tmp/out")"
  expect_log '^pct destroy 101'
  expect_log '^pct create 101 '
  expect_out "+ template 101"
}

test_refuses_a_guest_that_is_not_a_ghrm_template() {
  printf '102  running\n' >"$FAKE_STATE/guests"
  if install --template-vmid 102; then fail "a foreign guest must be refused"; fi
  expect_out "102 is not a ghrm template"
  expect_no_log 'pct (exec|push|destroy|set|start) 102'
}

test_a_new_release_is_installed() {
  install || fail "first run: exit $?"
  binaries v2.0.0
  : >"$FAKE_LOG"
  install || fail "upgrade: exit $?"
  expect_out "+ ghrm v2.0.0"
  expect_log 'pct push 100 \S+ /usr/local/bin/ghrm '
  expect_log 'systemctl restart ghrm'
}

ct_file() { cat "$FAKE_STATE/files/$1$2" 2>/dev/null; } # ct_file VMID PATH: a file inside a container

test_cache_is_created() {
  install || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_out "+ container 102 (registry cache"
  expect_log '^pct create 102 local:vztmpl/debian-13-standard.* --hostname ghrm-cache .*--rootfs local-lvm:100 --net0 name=eth0,bridge=jobnet,ip=10.50.0.3/24,gw=10.50.0.1'
  expect_no_log '^pct create 102 .*--pool'
  ct_file 102 /etc/ghrm-cache/docker.io.yml | grep -q 'remoteurl: https://registry-1.docker.io' || fail "docker.io proxy configuration"
  ct_file 102 /etc/ghrm-cache/mcr.microsoft.com.yml | grep -q 'addr: ":5002"' || fail "mcr port"
  ct_file 102 /etc/ghrm-cache/quay.io.yml | grep -q 'addr: ":5103"' || fail "quay metrics port"
  ct_file 102 /etc/systemd/system/ghrm-cache-registry@.service | grep -q 'registry serve /etc/ghrm-cache/%i.yml' || fail "registry unit"
  ct_file 102 /etc/systemd/system/ghrm-cache-prune.service | grep -q 'cache-prune --root /var/lib/ghrm-cache --budget-gb 90' || fail "prune unit (90% of the disk)"
  ct_file 102 /etc/systemd/system/ghrm-cache-exporter.service | grep -q 'cache-exporter --listen :5199' || fail "exporter unit"
  ct_file 102 /usr/local/bin/registry | grep -q '3.1.2' || fail "registry binary"
  ct_file 100 /etc/ghrm/ghrm.yaml | grep -q '^  address: 10.50.0.3' || fail "control plane configuration lacks the cache"
  grep -qx 10.50.0.3 "$FAKE_STATE/group_ghrm-job" || fail "security group lacks the cache rule"
  last_two=$(grep 'pvesh create /cluster/firewall/groups/ghrm-job' "$FAKE_LOG" | tail -n 2 | head -n 1)
  [[ $last_two == *"--dest 10.50.0.3 --dport 5000:5003"* ]] || fail "the cache rule must sit with the ingest rule above the drops, got: $last_two"
}

test_cache_rerun_changes_nothing() {
  install || fail "first run: exit $?"
  : >"$FAKE_LOG"
  install || fail "second run: exit $?: $(tail -n 3 "$tmp/out")"
  expect_no_log '^pct (create|push) 102'
  expect_no_log 'cat > /etc/ghrm-cache'
  expect_out "✓ container 102 (registry cache)"
  expect_no_out '^  \+ .*cache'
}

test_existing_group_gets_the_cache_rule_once() {
  seed_dev_host
  install --vmid 310 --security-group gh-runner || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_log 'pvesh create /cluster/firewall/groups/gh-runner --type out --action ACCEPT --proto tcp --dest 10.50.0.3 --dport 5000:5003 .*--pos 0'
  expect_out "+ cache rule in security group gh-runner"
  ct_file 310 /etc/ghrm/ghrm.yaml | grep -q '^cache:' || fail "the cache section must be added to an existing configuration"
  : >"$FAKE_LOG"
  install --vmid 310 --security-group gh-runner || fail "rerun: exit $?"
  expect_no_log 'pvesh create /cluster/firewall'
  expect_no_log 'cat >> /etc/ghrm/ghrm.yaml'
}

test_no_cache_skips_it() {
  install --no-cache || fail "exit $?"
  expect_no_log 'ghrm-cache'
  ct_file 100 /etc/ghrm/ghrm.yaml | grep -q '^cache:' && fail "no cache section without a cache"
  ! grep -qx 10.50.0.3 "$FAKE_STATE/group_ghrm-job" || fail "no cache rule without a cache"
}

test_dockerhub_token_never_touches_the_host_disk() {
  echo "dckr_pat_secret123" | PATH="$here/fakebin:$PATH" bash "$installer" --binary-dir "$tmp/bin" --dockerhub-user bob >"$tmp/out" 2>&1 || fail "exit $?: $(tail -n 3 "$tmp/out")"
  ct_file 102 /etc/ghrm-cache/docker.io.yml | grep -q 'password: dckr_pat_secret123' || fail "the credential belongs in the cache's docker.io configuration"
  ct_file 102 /etc/ghrm-cache/docker.io.yml | grep -q 'username: bob' || fail "username"
  if grep -rl dckr_pat_secret123 "$tmp" | grep -v "^$FAKE_STATE/files/"; then fail "the token was written outside the cache container"; fi
  ! grep -q dckr_pat_secret123 "$tmp/out" || fail "the token was printed"
}

test_container_images_match_the_host_architecture() {
  printf 'local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst\nlocal:vztmpl/debian-13-standard_13.6-1_arm64.tar.zst\n' >"$FAKE_STATE/images"
  install || fail "exit $?"
  expect_no_log '^pct create .*_arm64'
  expect_log '^pct create 100 local:vztmpl/debian-13-standard_13.6-1_amd64'
  expect_log '^pveam download local ubuntu-24.04-standard_24.04-2_amd64'
}

test_install_refuses_other_architectures() {
  if FAKE_ARCH=arm64 install; then fail "an arm64 host must be refused"; fi
  expect_out "amd64 binaries only (this host is arm64)"
}

test_install_refuses_old_pve() {
  if FAKE_PVEVERSION="pve-manager/8.4.1/abc (running kernel: 6.8)" install; then fail "a Proxmox VE 8 host must be refused"; fi
  expect_out "Proxmox VE 9.1 or later is required (found 8.4)"
  expect_no_log "$CREATES"
}

test_dry_run_changes_nothing() {
  install --dry-run || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_no_log "$CREATES"
  expect_out "would run: pveum pool add ghrm"
  expect_out "run without --dry-run to make the changes above"
  [ -z "$(cat "$FAKE_STATE"/pools "$FAKE_STATE"/guests 2>/dev/null)" ] || fail "state changed"
}

test_dry_run_on_a_complete_host_reports_nothing_to_do() {
  seed_dev_host
  install --dry-run --vmid 310 --security-group gh-runner --no-cache || fail "exit $?"
  expect_out "everything is in place"
  expect_no_out "would run: (pveum (pool|role|user)|pvesm|pvesh create|pct create)"
}

test_embedded_units_match_the_repository() {
  extract() { sed -n "/^$1='/,/'\$/p" "$installer" | sed -e "1s/^$1='//" -e "\$s/'\$//"; }
  diff <(extract GHRM_SERVICE) "$repo/deploy/systemd/ghrm.service" >/dev/null || fail "GHRM_SERVICE differs from deploy/systemd/ghrm.service"
  diff <(extract GHRM_AGENT_SERVICE) "$repo/template/layer/ghrm-agent.service" >/dev/null || fail "GHRM_AGENT_SERVICE differs from template/layer/ghrm-agent.service"
}

for t in $(declare -F | awk '{print $3}' | grep '^test_'); do
  count=$((count + 1))
  echo "$t"
  setup
  before=$failures
  $t
  if [ $failures -gt $before ]; then echo "  --- output:"; sed 's/^/    /' "$tmp/out" | tail -n 25; fi
  rm -rf "$tmp"
done
echo "$count tests, $failures failures"
[ $failures = 0 ]
