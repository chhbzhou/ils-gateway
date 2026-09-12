#!/bin/sh
set -eu

# Execute the production start/stop functions with only OpenWrt service,
# firewall, PKI and daemon boundaries mocked. The calls below are real
# start_service()/stop_service() invocations, not text-only assertions.
tmp=$(mktemp -d)
tmp_posix=$(printf '%s' "$tmp" | tr '\\' '/')
trap 'rm -rf "$tmp_posix"' EXIT
mkdir -p "$tmp_posix/bin" "$tmp_posix/state" "$tmp_posix/run" "$tmp_posix/nft" "$tmp_posix/activity"
root=$(cd "$(dirname "$0")/.." && pwd)
printf '%s\n' "config ios_location_spoofer 'main'" > "$tmp_posix/config"

cat > "$tmp_posix/functions.sh" <<'EOF'
config_load() { :; }
config_get() {
    var=$1
    option=$3
    value=${MOCK_ENABLED:-0}
    [ "$option" = enabled ] || value=
    eval "$var=\$value"
}
EOF

cat > "$tmp_posix/bin/ctl" <<'EOF'
#!/bin/sh
set -eu
cmd=${1-}
printf '%s\n' "$cmd" >> "$MOCK_LOG"
sets='ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6 ios_monitored_v4 ios_monitored_v6 ios_monitored_mac ios_online_v4 ios_online_v6 ios_online_mac ios_target_v4 ios_target_v6 ios_lan_ifaces'
case "$cmd" in
    clear)
        for name in $sets; do
            if [ -e "$MOCK_NFT/$name" ]; then
                [ "${MOCK_CLEAR_FAIL:-0}" = 1 ] && exit 1
                : > "$MOCK_NFT/$name"
            fi
        done
        ;;
    sets_ready)
        for name in $sets; do [ -e "$MOCK_NFT/$name" ] || exit 1; done
        ;;
    sync_monitor) ;;
    refresh_targets)
        printf '%s\n' 203.0.113.5 > "$MOCK_NFT/ios_target_v4"
        ;;
    apply)
        printf '1\n' > "$LOCSPOOF_STATE_DIR/nft_applied"
        ;;
    *) ;;
esac
EOF
chmod +x "$tmp_posix/bin/ctl"

cat > "$tmp_posix/bin/fw4" <<'EOF'
#!/bin/sh
set -eu
[ "${1-}" = reload ] || exit 1
printf '%s\n' fw4_reload >> "$MOCK_LOG"
for name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6 ios_monitored_v4 ios_monitored_v6 ios_monitored_mac ios_online_v4 ios_online_v6 ios_online_mac ios_target_v4 ios_target_v6 ios_lan_ifaces; do
    : > "$MOCK_NFT/$name"
done
EOF
chmod +x "$tmp_posix/bin/fw4"

cat > "$tmp_posix/bin/locspoof-ca" <<'EOF'
#!/bin/sh
printf '%s\n' ca >> "$MOCK_LOG"
for name in ca-cert.pem ca-key.pem profile-signing-cert.pem profile-signing-key.pem ca.mobileconfig gsp-ssl.ls.apple.com.pem gspe1-ssl.ls.apple.com.pem gs-loc.apple.com.pem gs-loc-cn.apple.com.pem bluedot.is.autonavi.com.pem bluedot.is.autonavi.com.gds.alibabadns.com.pem; do
    : > "$LOCSPOOF_PKI_DIR/$name"
done
exit 0
EOF
chmod +x "$tmp_posix/bin/locspoof-ca"

cat > "$tmp_posix/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$tmp_posix/bin/logger"

export LOCSPOOF_FUNCTIONS="$tmp_posix/functions.sh"
export LOCSPOOF_CTL="$tmp_posix/bin/ctl"
export LOCSPOOF_CA="$tmp_posix/bin/locspoof-ca"
export LOCSPOOF_DAEMON_RUNDIR="$tmp_posix/run"
export LOCSPOOF_CONFIG_DIR="$tmp_posix/config-runtime"
export LOCSPOOF_STATE_DIR="$tmp_posix/state"
export LOCSPOOF_ACTIVITY_DIR="$tmp_posix/activity"
export LOCSPOOF_UCI_CONFIG="$tmp_posix/config"
export LOCSPOOF_PKI_DIR="$tmp_posix/pki"
export LOCSPOOF_BIN="$tmp_posix/bin/locspoofd"
export MOCK_LOG="$tmp_posix/log" MOCK_NFT="$tmp_posix/nft"
export PATH="$tmp_posix/bin:${PATH-}"
init="$root/package/ils-gateway/root/etc/init.d/ios-location-spoofer"

# The test overrides only platform ownership and Unix socket probes; the
# production start_service body and all ordering/error paths remain active.
. "$init"
daemon_dir_safe() { return 0; }
state_dir_safe() { return 0; }
activity_dir_safe() { return 0; }
pki_metadata_check() { return 0; }
pki_access_check() { return 0; }
daemon_ready() { return 0; }
pidof() { return 0; }
id() { return 0; }
adduser() { return 0; }
chown() { return 0; }
procd_open_instance() { MOCK_INSTANCE=${1:-default}; }
procd_set_param() {
    [ "${1-}" = command ] || return 0
    shift
    printf 'procd:%s:%s\n' "$MOCK_INSTANCE" "$*" >> "$MOCK_LOG"
}
procd_append_param() { :; }
procd_close_instance() { :; }

assert_no_runtime_calls() {
	! grep -E '^(ca|refresh_targets|apply|procd:)' "$MOCK_LOG" >/dev/null 2>&1
}

# Disabled with no fw4 sets: load the passive presence sets, but do not start
# the location data plane or touch PKI.
: > "$MOCK_LOG"
export MOCK_ENABLED=0
start_service
assert_no_runtime_calls
[ "$(grep -c '^fw4_reload$' "$MOCK_LOG")" -eq 1 ]
grep -Fx sync_monitor "$MOCK_LOG" >/dev/null
[ ! -d "$tmp_posix/pki" ] || { echo 'disabled start touched PKI' >&2; exit 1; }

# Disabled with stale policy and markers: all stale state is removed without a
# firewall reload, daemon, or CA invocation.
for name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6; do
    printf stale > "$MOCK_NFT/$name"
done
for marker in enabled bypass nft_applied target_refresh_ok target_last_refresh target_v4_required target_v6_required; do : > "$tmp_posix/state/$marker"; done
: > "$tmp_posix/run/ready"
: > "$tmp_posix/run/control.sock"
: > "$tmp_posix/run/bypass_on.request"
: > "$MOCK_LOG"
start_service
assert_no_runtime_calls
for name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6; do
    [ ! -s "$MOCK_NFT/$name" ] || exit 1
done
[ ! -e "$tmp_posix/run/bypass_on.request" ]
grep -Fx sync_monitor "$MOCK_LOG" >/dev/null

# Enabled first start with no sets: one fw4 reload creates them, then strict
# clear, PKI and targets are prepared before procd launches the single daemon
# and watchdog.
rm -f "$MOCK_NFT"/*
: > "$MOCK_LOG"
export MOCK_ENABLED=1
start_service
actual=$(grep -E '^(clear|fw4_reload|sets_ready|sync_monitor|ca|refresh_targets)$' "$MOCK_LOG")
[ "$actual" = "clear
sets_ready
fw4_reload
sets_ready
sync_monitor
clear
ca
refresh_targets" ] || { echo "unexpected enabled order: $actual" >&2; exit 1; }
grep -F 'procd:data-plane:' "$MOCK_LOG" | grep -F -- '--config' >/dev/null
grep -F 'procd:data-plane:' "$MOCK_LOG" | grep -F -- '--activity-file' >/dev/null
[ "$(grep -c '^procd:data-plane:' "$MOCK_LOG")" -eq 1 ]
! grep -E -- '--enrollment-only|--disable-enrollment|procd:enrollment:' "$MOCK_LOG" >/dev/null

# Existing sets and a real flush failure must fail startup, not be hidden by
# cleanup. Repeated stop remains idempotent.
printf stale > "$MOCK_NFT/ios_enabled_v4"
export MOCK_CLEAR_FAIL=1
if start_service; then
    echo 'flush failure unexpectedly allowed startup' >&2
    exit 1
fi
unset MOCK_CLEAR_FAIL
stop_service
stop_service

echo 'ios-location-spoofer init lifecycle mock tests passed'
