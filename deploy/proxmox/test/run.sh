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
  mkdir -p "$FAKE_STATE" "$tmp/bin"
  : >"$FAKE_LOG"
  echo fake >"$tmp/bin/ghrm"
  echo fake >"$tmp/bin/ghrm-agent"
}

install() { # install ARGS...: runs the installer, output in $tmp/out
  PATH="$here/fakebin:$PATH" bash "$installer" --binary-dir "$tmp/bin" "$@" >"$tmp/out" 2>&1
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
  expect_log 'pct exec 100 -- ghrm secret set --config /etc/ghrm/ghrm.yaml proxmox/token-secret'
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
  expect_no_out '^  \+ (pool|role|user|API token|storage|SDN|VNet|subnet|security|container|template|/etc/ghrm)'
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
  printf '310  running\n951 ghrm-template stopped\n' >"$FAKE_STATE/guests"
  printf '310 proxmox/token-secret\n' >"$FAKE_STATE/ct_secrets"
  printf '310 /etc/ghrm/ghrm.yaml\n310 /etc/ghrm/admin-token\n' >"$FAKE_STATE/ct_files"
  touch "$FAKE_STATE/dnsmasq"
}

test_install_reuses_existing_setup() {
  seed_dev_host
  install --vmid 310 --security-group gh-runner || fail "exit $?: $(tail -n 3 "$tmp/out")"
  expect_no_log "$CREATES"
  expect_out "✓ template 951" # found by its tag
  expect_out "✓ container 310"
  expect_no_out "warning"
}

test_missing_ingest_rule_is_reported() {
  seed_dev_host
  : >"$FAKE_STATE/group_gh-runner"
  install --vmid 310 --security-group gh-runner || fail "exit $?"
  expect_out "has no rule for the ingest (10.50.0.2:8443)"
  expect_no_log 'pvesh create /cluster/firewall'
}

test_token_without_its_secret_is_created_again() {
  seed_dev_host
  : >"$FAKE_STATE/ct_secrets"
  install --vmid 310 --security-group gh-runner || fail "exit $?"
  expect_log 'pveum user token remove ghrm@pve ghrm'
  expect_log 'pveum user token add ghrm@pve ghrm --privsep 0'
  expect_log 'pct exec 310 -- ghrm secret set'
}

test_token_in_a_secret_file_is_kept() {
  seed_dev_host
  : >"$FAKE_STATE/ct_secrets"
  echo "310 token_secret_file" >"$FAKE_STATE/ct_config" # the config names a secret file that exists
  install --vmid 310 --security-group gh-runner || fail "exit $?"
  expect_no_log 'pveum user token (remove|add)'
  expect_out "✓ API token ghrm@pve!ghrm"
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
  install --dry-run --vmid 310 --security-group gh-runner || fail "exit $?"
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
