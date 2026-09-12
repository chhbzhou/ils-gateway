#!/bin/sh
set -eu

# Deterministic, single-iteration watchdog tests. The production watchdog
# accepts command and run-directory overrides solely to make this state
# machine testable without a router or a running procd instance.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp_posix"' EXIT
mkdir -p "$tmp/bin" "$tmp/run" "$tmp/state"
tmp_posix=$(printf '%s' "$tmp" | tr '\\' '/')
original_path=${PATH-}
test_path="$tmp_posix/bin:$original_path"

if [ -n "${LOCSPOOF_TEST_ROOT:-}" ]; then
	root=$LOCSPOOF_TEST_ROOT
else
	root=$(cd "$(dirname "$0")/.." && pwd)
fi
watchdog=$root/package/ils-gateway/root/usr/bin/locspoof-watchdog
log_posix=$tmp_posix/log
log=$log_posix
touch "$tmp_posix/state/enabled" "$tmp_posix/run/ready"

cat >"$tmp/bin/pidof" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$tmp/bin/health" <<'EOF'
#!/bin/sh
[ "${MOCK_HEALTH_FAIL:-0}" = 1 ] && exit 1
exit 0
EOF
cat >"$tmp/bin/fw4" <<'EOF'
#!/bin/sh
printf 'fw4 %s\n' "$*" >> "$MOCK_LOG"
[ "${MOCK_FW4_FAIL:-0}" = 1 ] && exit 1
: > "$MOCK_FW4_READY"
exit 0
EOF
cat >"$tmp/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$tmp/bin/ctl" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$MOCK_LOG"
case "$1" in
  clear)
    : > "$MOCK_CLEARED"
    rm -f "$MOCK_STATE_DIR/nft_applied"
    ;;
  bypass_on)
    [ "${MOCK_BYPASS_FAIL:-0}" = 1 ] && exit 1
    : > "$MOCK_STATE_DIR/bypass"
    ;;
  sets_ready)
    [ "${MOCK_SETS_READY:-1}" = 1 ] || [ -e "$MOCK_FW4_READY" ] || {
      [ "${MOCK_REQUEST_ON_SET_FAIL:-0}" = 1 ] && : > "$MOCK_RUNDIR/bypass_on.request"
      exit 1
    }
    ;;
  targets_ready)
    [ "${MOCK_TARGETS_READY:-1}" = 1 ] || [ -e "$MOCK_TARGETS_RESTORED" ]
    ;;
  target_refresh_due)
    [ "${MOCK_TARGETS_READY:-1}" != 1 ] || [ "${MOCK_TARGET_DUE:-0}" = 1 ]
    ;;
  refresh_targets)
    [ "${MOCK_REFRESH_FAIL:-0}" = 0 ] || exit 1
    : > "$MOCK_TARGETS_RESTORED"
    ;;
  verify)
    [ "${MOCK_VERIFY:-1}" = 1 ]
    ;;
  apply)
    [ "${MOCK_APPLY_FAIL:-0}" = 0 ] || exit 1
    : > "$MOCK_STATE_DIR/nft_applied"
    ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$tmp/bin"/*

run_case() {
    name=$1
    shift
    rm -f "$log_posix" "$tmp_posix/cleared" "$tmp_posix/state/enabled" "$tmp_posix/state/nft_applied"
    [ "${MOCK_NO_MARKER:-0}" = 1 ] || : > "$tmp_posix/state/enabled"
    : > "$tmp_posix/run/ready"
    rm -f "$tmp_posix/run/bypass_on.request" "$tmp_posix/state/bypass"
    [ "${MOCK_REQUEST:-0}" = 1 ] && : > "$tmp_posix/run/bypass_on.request"
    [ "${MOCK_EXISTING_MARKER:-0}" = 1 ] && : > "$tmp_posix/state/nft_applied"
    rm -f "$tmp_posix/fw4-ready" "$tmp_posix/targets-restored"
    MOCK_LOG=$log_posix MOCK_CLEARED=$tmp_posix/cleared MOCK_RUNDIR=$tmp_posix/run MOCK_STATE_DIR=$tmp_posix/state \
    MOCK_FW4_READY=$tmp_posix/fw4-ready \
    MOCK_TARGETS_RESTORED=$tmp_posix/targets-restored \
    MOCK_SETS_READY=${MOCK_SETS_READY:-1} MOCK_VERIFY=${MOCK_VERIFY:-1} \
    MOCK_FW4_FAIL=${MOCK_FW4_FAIL:-0} MOCK_APPLY_FAIL=${MOCK_APPLY_FAIL:-0} \
    MOCK_HEALTH_FAIL=${MOCK_HEALTH_FAIL:-0} \
    MOCK_BYPASS_FAIL=${MOCK_BYPASS_FAIL:-0} \
    MOCK_TARGETS_READY=${MOCK_TARGETS_READY:-1} MOCK_TARGET_DUE=${MOCK_TARGET_DUE:-0} MOCK_REFRESH_FAIL=${MOCK_REFRESH_FAIL:-0} \
    MOCK_REQUEST_ON_SET_FAIL=${MOCK_REQUEST_ON_SET_FAIL:-0} \
    LOCSPOOF_SKIP_SOCKET_CHECK=1 \
    LOCSPOOF_RUNDIR=$tmp_posix/run LOCSPOOF_STATE_DIR=$tmp_posix/state LOCSPOOF_CTL=$tmp_posix/bin/ctl \
    LOCSPOOF_HEALTH=$tmp_posix/bin/health LOCSPOOF_FW4=$tmp_posix/bin/fw4 \
    LOCSPOOF_PIDOF=$tmp_posix/bin/pidof LOCSPOOF_LOGGER=$tmp_posix/bin/logger \
    LOCSPOOF_STARTUP_ATTEMPTS=1 LOCSPOOF_STARTUP_DELAY=0 \
    LOCSPOOF_WATCHDOG_ONCE=1 PATH=$test_path "$@"
}

# A fresh procd start has no marker. The root watchdog waits for the daemon,
# creates the marker atomically, and applies policy only after health succeeds.
MOCK_NO_MARKER=1 MOCK_SETS_READY=1 MOCK_VERIFY=1 run_case activate sh "$watchdog"
[ -e "$tmp/state/enabled" ]
grep '^apply$' "$log" >/dev/null

# Existing marker and verified sets must not cause a redundant apply.
MOCK_EXISTING_MARKER=1 MOCK_SETS_READY=1 MOCK_VERIFY=1 run_case existing sh "$watchdog"
grep '^sets_ready$' "$log" >/dev/null
grep '^verify$' "$log" >/dev/null
! grep '^apply$' "$log" >/dev/null

# A request created after startup is consumed exactly once and converted to
# the fail-open bypass marker. It must not remain for a later restart.
MOCK_REQUEST=1 MOCK_SETS_READY=1 MOCK_VERIFY=1 run_case request sh "$watchdog"
grep '^bypass_on$' "$log" >/dev/null
[ ! -e "$tmp/run/bypass_on.request" ]
[ -e "$tmp/state/bypass" ]

# Fail-open cleanup must preserve a request which races with a daemon failure.
MOCK_SETS_READY=0 MOCK_FW4_FAIL=1 MOCK_REQUEST_ON_SET_FAIL=1 run_case request-cleanup sh "$watchdog"
[ -e "$tmp/run/bypass_on.request" ]

# A failed bypass operation leaves the request for retry and keeps policy
# fail-open; a successful operation consumes it only after the marker exists.
MOCK_REQUEST=1 MOCK_BYPASS_FAIL=1 MOCK_SETS_READY=1 run_case bypass-fail sh "$watchdog"
[ -e "$tmp/run/bypass_on.request" ]
[ ! -e "$tmp/state/bypass" ]
MOCK_REQUEST=1 MOCK_SETS_READY=1 run_case bypass-success sh "$watchdog"
[ ! -e "$tmp/run/bypass_on.request" ]
[ -e "$tmp/state/bypass" ]

# Target sets can be present but empty after fw4 reload. Refresh targets and
# actively resolve targets before applying devices.
MOCK_SETS_READY=1 MOCK_TARGETS_READY=0 MOCK_VERIFY=1 run_case target-empty sh "$watchdog"
grep '^targets_ready$' "$log" >/dev/null
grep '^refresh_targets$' "$log" >/dev/null
grep '^apply$' "$log" >/dev/null

# A populated target set still needs a periodic refresh once the bounded
# refresh interval expires. This covers the path where both target families
# are currently non-empty and prevents a stale nft timeout from being used
# indefinitely.
MOCK_SETS_READY=1 MOCK_TARGETS_READY=1 MOCK_TARGET_DUE=1 MOCK_VERIFY=1 run_case refresh-due sh "$watchdog"
grep '^refresh_targets$' "$log" >/dev/null
grep '^apply$' "$log" >/dev/null

# fw4 can recreate empty sets. The watchdog reloads it and reapplies only
# after the sets become available and the daemon remains healthy.
MOCK_SETS_READY=0 MOCK_TARGETS_READY=0 MOCK_VERIFY=1 run_case reload sh "$watchdog"
grep '^fw4 reload$' "$log" >/dev/null
grep '^refresh_targets$' "$log" >/dev/null
grep '^apply$' "$log" >/dev/null
[ -e "$tmp/state/nft_applied" ]

# A failed firewall reload must leave all runtime state fail-open.
MOCK_SETS_READY=0 MOCK_FW4_FAIL=1 run_case reload-fail sh "$watchdog"
grep '^fw4 reload$' "$log" >/dev/null
[ -e "$tmp/cleared" ]
[ ! -e "$tmp/state/nft_applied" ]

# A target refresh failure is also fail-open after fw4 rebuilt
# the table, because target sets would otherwise be stale.
MOCK_SETS_READY=1 MOCK_TARGETS_READY=0 MOCK_REFRESH_FAIL=1 run_case target-refresh-fail sh "$watchdog"
grep '^refresh_targets$' "$log" >/dev/null
[ -e "$tmp/cleared" ]
[ ! -e "$tmp/run/nft_applied" ]

# bypass is authoritative: no fw4 reload and no apply while it is set.
touch "$tmp/state/bypass"
MOCK_SETS_READY=0 run_case bypass sh "$watchdog"
! grep '^apply$' "$log" >/dev/null
! grep '^fw4 reload$' "$log" >/dev/null
[ -e "$tmp/cleared" ]
rm -f "$tmp/state/bypass"

# apply followed by a failed verify is rolled back and does not leave a
# misleading nft_applied marker.
MOCK_SETS_READY=1 MOCK_VERIFY=0 run_case verify-fail sh "$watchdog"
grep '^apply$' "$log" >/dev/null
[ -e "$tmp/cleared" ]
[ ! -e "$tmp/state/nft_applied" ]

echo "locspoof-watchdog state-machine mock tests passed"
