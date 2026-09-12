#!/bin/sh
set -eu

# The rc.common script cannot be executed on a workstation without procd.
# Keep its security and fail-open invariants machine-checkable instead of
# silently skipping init coverage in desktop CI.
if [ -n "${LOCSPOOF_TEST_ROOT:-}" ]; then
	root=$LOCSPOOF_TEST_ROOT
else
	root=$(cd "$(dirname "$0")/.." && pwd)
fi
init=$root/package/ils-gateway/root/etc/init.d/ios-location-spoofer

assert_has() {
    needle=$1
    grep -F "$needle" "$init" >/dev/null || {
        echo "init missing: $needle" >&2
        exit 1
    }
}

assert_has 'ensure_fw4_sets()'
assert_has 'STATE_DIR=${LOCSPOOF_STATE_DIR:-/var/lib/locspoofd}'
assert_has 'chown root:root "$STATE_DIR"'
assert_has 'DAEMON_RUNDIR=${LOCSPOOF_DAEMON_RUNDIR:-/var/run/locspoofd}'
assert_has 'CONFIG_DIR=${LOCSPOOF_CONFIG_DIR:-/var/run/locspoofd-config}'
assert_has 'CONFIG_FILE="$CONFIG_DIR/ios-location-spoofer"'
assert_has 'UCI_CONFIG=${LOCSPOOF_UCI_CONFIG:-/etc/config/ios-location-spoofer}'
assert_has '[ -z "${IPKG_INSTROOT:-}" ] || return 1'
assert_has 'CTL=${LOCSPOOF_CTL:-/usr/bin/locspoofctl}'
assert_has '"$CTL" sets_ready'
assert_has '"$CTL" sync_monitor'
assert_has 'fw4 reload || return 1'
assert_has 'pki_metadata_check()'
assert_has 'pki_access_check()'
assert_has 'prepare_runtime_config()'
assert_has 'chown root:locspoofd "$CONFIG_DIR"'
assert_has 'chmod 0750 "$CONFIG_DIR"'
assert_has 'chown root:locspoofd "$tmp"'
assert_has 'chmod 0640 "$tmp"'
assert_has 'procd_set_param command "$BIN" --config "$CONFIG_FILE"'
assert_has 'procd_open_instance data-plane'
if grep -E -- '--enrollment-only|--disable-enrollment' "$init" >/dev/null; then
    echo 'init still splits certificate enrollment into another process' >&2
    exit 1
fi
if grep -F 'procd_add_reload_trigger ios-location-spoofer' "$init" >/dev/null; then
    echo 'init retains duplicate automatic reload trigger' >&2
    exit 1
fi
if sed -n '/^start_service()/,/^}/p' "$init" | grep -F 'daemon_ready' >/dev/null 2>&1; then
    echo 'start_service waits for a procd instance before procd can launch it' >&2
    exit 1
fi
assert_has 'su -s /bin/sh -c'
assert_has 'PKI_DIR=${LOCSPOOF_PKI_DIR:-/etc/locspoof/pki}'
assert_has "PKI_HOSTS='gsp-ssl.ls.apple.com gspe1-ssl.ls.apple.com gs-loc.apple.com gs-loc-cn.apple.com bluedot.is.autonavi.com bluedot.is.autonavi.com.gds.alibabadns.com'"
assert_has 'ACTIVITY_DIR=${LOCSPOOF_ACTIVITY_DIR:-/var/lib/locspoofd-activity}'
assert_has 'activity_dir_safe()'
assert_has 'chown locspoofd:locspoofd "$ACTIVITY_DIR"'
assert_has 'chmod 0750 "$PKI_DIR"'
assert_has 'chmod 0600 "$PKI_DIR/ca-key.pem"'
assert_has 'profile-signing-cert.pem'
assert_has 'profile-signing-key.pem'
assert_has 'ca.mobileconfig'
assert_has 'clear_startup_state'
assert_has 'package user locspoofd is missing'
assert_has 'clear_bypass_request()'
assert_has 'rm -f "$DAEMON_RUNDIR/bypass_on.request"'
start_clear_count=$(sed -n '/^start_service()/,/^}/p' "$init" | grep -c 'clear_startup_state' || true)
stop_clear_count=$(sed -n '/^stop_service()/,/^}/p' "$init" | grep -c 'clear_bypass_request' || true)
[ "$start_clear_count" -ge 1 ] || { echo 'start_service does not perform startup fail-open cleanup' >&2; exit 1; }
[ "$stop_clear_count" -ge 1 ] || { echo 'stop_service does not clear bypass requests' >&2; exit 1; }
assert_has '"$CTL" clear'
if grep -nE 'DNS_SCRIPT|dnsmasq-fragment|dnsmasq-full' "$init" >/dev/null; then
    echo 'init retains removed dnsmasq fragment lifecycle' >&2
    exit 1
fi

# Runtime nft/firewall failures must be visible to procd and cannot be
# replaced by a swallowed success.
if grep -nE 'fw4 reload.*\|\| true|locspoofctl (clear|apply).*\|\| true' "$init" >/dev/null; then
    echo 'init masks a firewall/control failure' >&2
    exit 1
fi
if grep -nE 'adduser.*locspoofd' "$init" >/dev/null; then
    echo 'init tries to create a package user at service start' >&2
    exit 1
fi

ctl=$root/package/ils-gateway/root/usr/bin/locspoofctl
if grep -nE '/tmp/[^ ]*\.\$\$' "$ctl" >/dev/null; then
    echo 'locspoofctl uses predictable temporary file names' >&2
    exit 1
fi
grep -F 'mktemp -d' "$ctl" >/dev/null

echo 'ios-location-spoofer init static checks passed'
