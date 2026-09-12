#!/bin/sh
set -eu

# Repeatable mock-nft regression tests for clear, MAC-only IPv6 blocking and
# transactional apply cleanup. The fake UCI and nft implementations are kept
# deliberately small so these tests also run on a development workstation.
script_dir=$(dirname "$0")
if [ -n "${LOCSPOOF_TEST_ROOT:-}" ]; then
    root=$LOCSPOOF_TEST_ROOT
else
    # Avoid `cd --` here: the Windows BusyBox build treats the option as an
    # empty path. The path is controlled by this script, so `cd` is safe.
    root=$(cd "$script_dir/.." && pwd)
fi
tmp=$(mktemp -d)
cleanup_test() { [ "${LOCSPOOF_TEST_KEEP:-0}" = 1 ] || rm -rf "$tmp"; }
trap cleanup_test EXIT
tmp_posix=$(printf '%s' "$tmp" | tr '\\' '/')
mkdir -p "$tmp/bin" "$tmp/state" "$tmp/run" "$tmp/markers"

cat >"$tmp/functions.sh" <<'EOF'
config_load() { :; }
config_get() {
    var=$1; section=$2; option=$3; default=${4-}; value=$default
    case "$section:$option" in
        managed1:enabled) value=${MOCK_MANAGED1_ENABLED-1} ;;
        managed1:mac) value=${MOCK_MANAGED1_MAC-} ;;
        managed1:ip) value=${MOCK_MANAGED1_IP-} ;;
        managed2:enabled) value=${MOCK_MANAGED2_ENABLED-1} ;;
        managed2:mac) value=${MOCK_MANAGED2_MAC-} ;;
        managed2:ip) value=${MOCK_MANAGED2_IP-} ;;
        *:device_mac) value=${MOCK_DEVICE_MAC-} ;;
        *:device_ipv4) value=${MOCK_DEVICE_IPV4-} ;;
        *:device_ipv6) value=${MOCK_DEVICE_IPV6-} ;;
        *:ipv6_block) value=${MOCK_IPV6_BLOCK-0} ;;
    esac
    case "$option" in
        device_mac) value=${MOCK_DEVICE_MAC-} ;;
        device_ipv4) value=${MOCK_DEVICE_IPV4-} ;;
        device_ipv6) value=${MOCK_DEVICE_IPV6-} ;;
        ipv6_block) value=${MOCK_IPV6_BLOCK-0} ;;
    esac
    eval "$var=\${value}"
}
config_list_foreach() {
    list_section=$1
    list_option=$2
    list_function=$3
    shift 3
    values=
    case "$list_section:$list_option" in
        managed1:ips) values=${MOCK_MANAGED1_IPS-} ;;
        managed2:ips) values=${MOCK_MANAGED2_IPS-} ;;
        *:device_ips) values=${MOCK_DEVICE_IPS-} ;;
        *:device_macs) values=${MOCK_DEVICE_MACS-} ;;
        *:lan_interface) values=${MOCK_LAN_INTERFACES-'br-lan'} ;;
        *:wloc_host) values=${MOCK_WLOC_HOSTS-'gsp-ssl.ls.apple.com gspe1-ssl.ls.apple.com gs-loc.apple.com gs-loc-cn.apple.com bluedot.is.autonavi.com bluedot.is.autonavi.com.gds.alibabadns.com'} ;;
    esac
    case "$list_option" in
        device_ips) values=${MOCK_DEVICE_IPS-} ;;
        device_macs) values=${MOCK_DEVICE_MACS-} ;;
        lan_interface) values=${MOCK_LAN_INTERFACES-'br-lan'} ;;
        wloc_host) values=${MOCK_WLOC_HOSTS-'gsp-ssl.ls.apple.com gspe1-ssl.ls.apple.com gs-loc.apple.com gs-loc-cn.apple.com bluedot.is.autonavi.com bluedot.is.autonavi.com.gds.alibabadns.com'} ;;
    esac
    for value in $values; do
        "$list_function" "$value" "$@"
    done
}
config_foreach() {
    foreach_function=$1; foreach_type=$2
    [ "$foreach_type" = device ] || return 0
    for foreach_section in ${MOCK_DEVICE_SECTIONS-}; do
        "$foreach_function" "$foreach_section"
    done
}
EOF

cat >"$tmp/bin/nft" <<'EOF'
#!/bin/sh
set -eu
state=${NFT_STATE:?}
log=${NFT_LOG:?}
printf '%s\n' "$*" >> "$log"

if [ "${1-}" = "-f" ]; then
    batch=${2-}
    [ -r "$batch" ] || exit 1
    work="$state.tmp.$$"
    rm -rf "$work"; mkdir -p "$work"
    cp -f "$state"/* "$work" 2>/dev/null || :
    while IFS=' ' read -r command rest; do
        case "$command $rest" in
            flush\ set\ inet\ fw4\ ios_target_v4) : > "$work/ios_target_v4" ;;
            flush\ set\ inet\ fw4\ ios_target_v6) : > "$work/ios_target_v6" ;;
            add\ element\ inet\ fw4\ ios_target_v4*)
                [ "${FAIL_BATCH-}" != 1 ] || { rm -rf "$work"; exit 1; }
                printf '%s\n' "$rest" | sed 's/.*{ //; s/ }.*/ /' | tr ',' '\n' | sed 's/^ *//; s/ *$//' | while IFS= read -r value; do
                    [ -n "$value" ] || continue
                    grep -Fx "$value" "$work/ios_target_v4" >/dev/null 2>&1 && { rm -rf "$work"; exit 1; }
                    printf '%s\n' "$value" >> "$work/ios_target_v4"
                done ;;
            add\ element\ inet\ fw4\ ios_target_v6*)
                [ "${FAIL_BATCH-}" != 1 ] || { rm -rf "$work"; exit 1; }
                printf '%s\n' "$rest" | sed 's/.*{ //; s/ }.*/ /' | tr ',' '\n' | sed 's/^ *//; s/ *$//' | while IFS= read -r value; do
                    [ -n "$value" ] || continue
                    grep -Fx "$value" "$work/ios_target_v6" >/dev/null 2>&1 && { rm -rf "$work"; exit 1; }
                    printf '%s\n' "$value" >> "$work/ios_target_v6"
                done ;;
        esac
    done < "$batch"
    rm -rf "$state"; mv "$work" "$state"
    exit 0
fi

canon() {
    value=$1
    case "$value" in
        2001:0db8:0000:0000:0000:0000:0000:0001|2001:db8:0:0:0:0:0:1|2001:db8::1)
            printf '%s\n' '2001:db8::1' ;;
        *) printf '%s\n' "$value" | tr 'A-F' 'a-f' ;;
    esac
}

set_name=${4-}
case "${1-}" in
-j)
    # Structured nft JSON used by verify_exact_set. Keep this mock strict:
    # membership tests must not fall back to textual list parsing.
    shift
    [ "${1-}" = list ] && [ "${2-}" = set ] || exit 1
    name=${5-}
    [ -f "$state/$name" ] || exit 1
    if [ -n "${NFT_FIXTURE:-}" ] && [ -r "$NFT_FIXTURE" ] && { [ -z "${NFT_FIXTURE_SET:-}" ] || [ "${NFT_FIXTURE_SET}" = "$name" ]; }; then
        sed "s/\"name\"[[:space:]]*:[[:space:]]*\"ios_enabled_v6\"/\"name\": \"$name\"/" "$NFT_FIXTURE"
        exit 0
    fi
    # Match nft 1.1 JSON emitted by OpenWrt: metainfo is the first item, set
    # members use set.elem with timeout metadata, and empty sets omit elem.
    if [ ! -s "$state/$name" ] && [ "${MOCK_JSON_PREFIX:-0}" != 1 ]; then
        printf '{"nftables":[{"metainfo":{"json_schema_version":1}},{"set":{"family":"inet","table":"fw4","name":"%s"}}]}\n' "$name"
        exit 0
    fi
    printf '{"nftables":[{"metainfo":{"json_schema_version":1}},{"set":{"family":"inet","table":"fw4","name":"%s","elem":[' "$name"
    sep=
    while IFS= read -r elem; do
        [ -n "$elem" ] || continue
        printf '%s\n{"elem":{"val":"%s","timeout":300,"expires":299}}' "$sep" "$elem"
        sep=,
    done < "$state/$name"
    if [ "${MOCK_JSON_PREFIX:-0}" = 1 ]; then
        printf ',\n{"prefix":{"addr":"2001:db8:1::","len":64},"timeout":300}'
    fi
    printf '\n]}}]}\n'
    exit 0 ;;
list)
    if [ "${2-}" = table ]; then
        [ "${FAIL_LIST_TABLE-}" != 1 ] || exit 1
        exit 0
    fi
    case "${5-}" in
      ios_enabled_v4|ios_enabled_v6|ios_enabled_mac|ios_enabled_mac_v6|ios_block_v6|ios_block_mac_v6|ios_monitored_v4|ios_monitored_v6|ios_monitored_mac|ios_online_v4|ios_online_v6|ios_online_mac|ios_target_v4|ios_target_v6|ios_lan_ifaces)
        [ -f "$state/${5}" ] || exit 1
        value=$(cat "$state/${5}")
        if [ -n "$value" ]; then
            value=$(printf '%s\n' "$value" | tr '\n' ',' | sed 's/,$//')
            printf 'table inet fw4 { set %s { elements = { %s } } }\n' "$5" "$value"
        else
            printf 'table inet fw4 { set %s { } }\n' "$5"
        fi
        exit 0 ;;
    esac
    exit 1 ;;
flush)
    set_name=${5-}
    if [ "${FAIL_FLUSH_SET-}" = "$set_name" ]; then exit 1; fi
    [ -f "$state/$set_name" ] || exit 1
    : > "$state/$set_name"
    exit 0 ;;
add)
    set_name=${5-}
    [ "${FAIL_ADD_SET-}" = "$set_name" ] && exit 1
	element=$(printf '%s' "${6-}" | tr -d '{}\"' | awk '{print $1}')
    element=$(canon "$element")
    touch "$state/$set_name"
    if grep -Fx "$element" "$state/$set_name" >/dev/null 2>&1; then
        printf '%s\n' 'nft: File exists' >&2
        exit 1
    fi
    printf '%s\n' "$element" >> "$state/$set_name"
    exit 0 ;;
get)
    if [ "${FAIL_GET-}" = 1 ]; then
        printf '%s\n' 'nft: Operation not permitted' >&2
        exit 1
    fi
    set_name=${5-}
    elements=$(printf '%s' "${6-}" | tr -d '{} ')
    [ -f "$state/$set_name" ] || exit 1
    old_ifs=$IFS; IFS=,
    for element in $elements; do
        element=$(canon "$element")
        grep -Fx "$element" "$state/$set_name" >/dev/null 2>&1 || { IFS=$old_ifs; exit 1; }
    done
    IFS=$old_ifs
    exit 0 ;;
esac
exit 1
EOF
chmod +x "$tmp/bin/nft"
cat >"$tmp/bin/jsonfilter" <<'EOF'
#!/bin/sh
set -eu
state=${NFT_STATE:?}
file=
expr=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -i) file=$2; shift 2 ;;
        -e) expr=$2; shift 2 ;;
        *) shift ;;
    esac
done
[ -r "$file" ] || exit 1
name=$(sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file")
printf 'jsonfilter %s\n' "$expr" >> "${JSONFILTER_LOG:?}"
element_line() {
    want=$1
    awk -v want="$want" 'BEGIN { inelem=0; n=0 }
      /"elem"[[:space:]]*:[[:space:]]*\[/ { inelem=1; next }
      inelem && /\][[:space:]}]*$/ && $0 !~ /\{/ { exit }
      inelem {
        if ($0 ~ /^[[:space:]]*"[^"]+"[,]?[[:space:]]*$/ || $0 ~ /^[[:space:]]*\{/ || $0 ~ /^[[:space:]]*\[/) {
          if (n == want) { print; exit }
          n++
        }
      }' "$file"
}
case "$expr" in
    '@.nftables') grep -F '"nftables"' "$file" >/dev/null ;;
    *set.name*) sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" ;;
    *set.family*) sed -n 's/.*"family"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" ;;
    *set.table*) sed -n 's/.*"table"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" ;;
    *set.elements*) grep -F '"elements"' "$file" >/dev/null && printf '%s\n' legacy ;;
    *set.elem*\[\*\]*)
        # One complete JSON value per wildcard result, matching OpenWrt
        # jsonfilter. This branch intentionally precedes field-specific paths.
        grep -F '"elem"' "$file" >/dev/null || exit 1
        if [ -n "${NFT_FIXTURE:-}" ] && [ -r "$NFT_FIXTURE" ] && { [ -z "${NFT_FIXTURE_SET:-}" ] || [ "${NFT_FIXTURE_SET}" = "$name" ]; }; then
            awk '/"elem"[[:space:]]*:[[:space:]]*\[/ { inelem=1; next } inelem && /^[[:space:]]*\][,}]*[[:space:]]*$/ { exit } inelem { line=$0; sub(/^[[:space:]]*/, "", line); sub(/,[[:space:]]*$/, "", line); if (line ~ /^"/ || line ~ /^\{/ || line ~ /^\[/ || line == "null" || line == "true" || line == "false") print line }' "$NFT_FIXTURE"
        else
            while IFS= read -r value; do
                [ -n "$value" ] || continue
                printf '{"elem":{"val":"%s","timeout":300,"expires":299}}\n' "$value"
            done < "$state/$name"
            if [ "${MOCK_JSON_PREFIX:-0}" = 1 ]; then
                printf '%s\n' '{"prefix":{"addr":"2001:db8:1::","len":64},"timeout":300}'
            fi
        fi
        exit 0 ;;
    *set.elem*prefix.addr*)
        if printf '%s' "$expr" | grep -F '[*]' >/dev/null 2>&1; then
            sed -n 's/.*"addr"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file"
            exit 0
        fi
        idx=$(printf '%s' "$expr" | sed -n 's/.*elem\[\([0-9][0-9]*\)\].*/\1/p'); [ -n "$idx" ] || idx=0
        element_line "$idx" | sed -n 's/.*"addr"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' ;;
    *set.elem*prefix.len*)
        if printf '%s' "$expr" | grep -F '[*]' >/dev/null 2>&1; then
            sed -n 's/.*"len"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$file"
            exit 0
        fi
        idx=$(printf '%s' "$expr" | sed -n 's/.*elem\[\([0-9][0-9]*\)\].*/\1/p'); [ -n "$idx" ] || idx=0
        element_line "$idx" | sed -n 's/.*"len"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' ;;
    *set.elem*elem.val*)
        if printf '%s' "$expr" | grep -F '[*]' >/dev/null 2>&1; then
            sed -n 's/.*"elem"[[:space:]]*:[[:space:]]*{[[:space:]]*"val"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file"
            exit 0
        fi
        idx=$(printf '%s' "$expr" | sed -n 's/.*elem\[\([0-9][0-9]*\)\].*/\1/p'); [ -n "$idx" ] || idx=0
        element_line "$idx" | sed -n 's/.*"elem"[[:space:]]*:[[:space:]]*{[[:space:]]*"val"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' ;;
    *set.elem*val*)
        if printf '%s' "$expr" | grep -F '[*]' >/dev/null 2>&1; then
            sed -n 's/.*"val"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file"
            exit 0
        fi
        idx=$(printf '%s' "$expr" | sed -n 's/.*elem\[\([0-9][0-9]*\)\].*/\1/p'); [ -n "$idx" ] || idx=0
        element_line "$idx" | sed -n 's/.*"val"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' ;;
    *set.elem*)
        idx=$(printf '%s' "$expr" | sed -n 's/.*elem\[\([0-9][0-9]*\)\].*/\1/p'); [ -n "$idx" ] || idx=0
        element_line "$idx" | sed 's/^[[:space:]]*//; s/[[:space:],]*$//' ;;
    *) exit 1 ;;
esac
EOF
chmod +x "$tmp/bin/jsonfilter"

ctl="$root/package/ils-gateway/root/usr/bin/locspoofctl"
original_path=${PATH-}
# Keep the caller's PATH intact. In particular, do not replace it with a
# machine-specific BusyBox directory: cat, rm and awk may live in
# MSYS/Git, while the mock commands live in this private directory.
PATH="$tmp_posix/bin:$original_path"
LOCSPOOF_FUNCTIONS="$tmp/functions.sh"
LOCSPOOF_RUNDIR="$tmp/run"
export PATH LOCSPOOF_FUNCTIONS LOCSPOOF_RUNDIR LOCSPOOF_STATE_DIR="$tmp/markers"
export NFT_STATE="$tmp/state" NFT_LOG="$tmp/nft.log" JSONFILTER_LOG="$tmp/jsonfilter.log" DNS_QUERY_LOG="$tmp/dns-queries.log"
export TMPDIR="$tmp_posix"
# Resolve the mock through PATH, matching the OpenWrt deployment. Passing a
# Windows `C:/...` absolute path to BusyBox `command -v` is not portable and
# makes a valid executable look absent.
# Resolve the mock through PATH. BusyBox on Windows does not reliably treat a
# C:/ absolute executable as `command -v` input, while OpenWrt uses PATH.
export LOCSPOOF_JSONFILTER=jsonfilter
export LOCSPOOF_TIMEOUT=timeout
export LOCSPOOF_TARGET_WAIT_STEPS=2 LOCSPOOF_TARGET_WAIT_SECONDS=0

# A fresh install may not have loaded fw4 yet. Missing sets are already
# fail-open and clear must report success while still attempting every name.
for set_name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6; do
    rm -f "$NFT_STATE/$set_name"
done
if ! sh "$ctl" clear; then
    echo 'clear treated absent nft sets as an error' >&2
    exit 1
fi

cat >"$tmp/bin/nslookup" <<'EOF'
#!/bin/sh
printf '%s\n' "$1" >> "$DNS_QUERY_LOG"
[ "${MOCK_DNS_FAIL:-0}" = 1 ] && exit 1
printf 'Server: 127.0.0.1\nAddress: 127.0.0.1#53\nName: %s\n' "$1"
[ "${MOCK_DNS_V4:-1}" = 1 ] && printf 'Address: 203.0.113.5\n'
[ "${MOCK_DNS_V6:-1}" = 1 ] && printf 'Address 1: 2001:db8::5\n'
exit 0
EOF
chmod +x "$tmp/bin/nslookup"

for set_name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6 ios_monitored_v4 ios_monitored_v6 ios_monitored_mac ios_online_v4 ios_online_v6 ios_online_mac ios_target_v4 ios_target_v6 ios_lan_ifaces; do
    : > "$NFT_STATE/$set_name"
done
printf '%s\n' br-lan > "$NFT_STATE/ios_lan_ifaces"
# Static fw4 target sets must contain at least one address before a device
# policy can be considered applicable. In production these are populated by
# refresh_targets; the mock seeds representative A/AAAA results.
printf '%s\n' '17.253.24.1' > "$NFT_STATE/ios_target_v4"
printf '%s\n' '2600:1408:ec00:36::1736:7f24' > "$NFT_STATE/ios_target_v6"
printf '%s\n' 198.51.100.1 > "$NFT_STATE/ios_target_v4"
export FAIL_FLUSH_SET=ios_enabled_v6
if sh "$ctl" clear; then
    echo "clear unexpectedly succeeded" >&2
    exit 1
fi
for set_name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6; do
    grep -F "flush set inet fw4 $set_name" "$NFT_LOG" >/dev/null || {
        echo "missing flush for $set_name" >&2
        exit 1
    }
done
unset FAIL_FLUSH_SET

export MOCK_DEVICE_MAC=02:00:00:00:00:01 MOCK_IPV6_BLOCK=1 MOCK_DEVICE_IPV4= MOCK_DEVICE_IPV6=
if ! sh "$ctl" apply; then
    echo "MAC-only IPv6 block apply failed" >&2
    exit 1
fi
[ -s "$NFT_STATE/ios_enabled_mac" ]
[ -s "$NFT_STATE/ios_block_mac_v6" ]
[ ! -s "$NFT_STATE/ios_enabled_mac_v6" ]
if ! sh "$ctl" verify; then
    echo "MAC-only IPv6 block verify failed" >&2
    exit 1
fi

# A dual-stack device must keep IPv4, IPv6 and MAC values in their separate
# sets, and verify must accept the exact current UCI policy.
export MOCK_IPV6_BLOCK=0 MOCK_DEVICE_IPV4=192.0.2.10 MOCK_DEVICE_IPV6=2001:db8::10
if ! sh "$ctl" apply || ! sh "$ctl" verify; then
    echo "mixed IPv4/IPv6/MAC apply or verify failed" >&2
    exit 1
fi
[ "$(cat "$NFT_STATE/ios_enabled_v4")" = "192.0.2.10" ]
[ "$(cat "$NFT_STATE/ios_enabled_v6")" = "2001:db8::10" ]
[ -s "$NFT_STATE/ios_enabled_mac_v6" ]
[ ! -s "$NFT_STATE/ios_block_mac_v6" ]

# The mock follows OpenWrt 24.10's callback contract and exercises list
# values one-by-one, including mixed address families and uppercase MACs.
export MOCK_DEVICE_MAC= MOCK_DEVICE_MACS='02:AA:BB:CC:DD:EF' MOCK_DEVICE_IPS='192.0.2.11 2001:db8::11' MOCK_DEVICE_IPV4= MOCK_DEVICE_IPV6= MOCK_IPV6_BLOCK=0
if ! sh "$ctl" apply || ! sh "$ctl" verify; then
    echo "list callback mixed-device configuration failed" >&2
    exit 1
fi
grep -Fx 192.0.2.11 "$NFT_STATE/ios_enabled_v4" >/dev/null
grep -Fx 2001:db8::11 "$NFT_STATE/ios_enabled_v6" >/dev/null
grep -Fx 02:aa:bb:cc:dd:ef "$NFT_STATE/ios_enabled_mac" >/dev/null

# Structured device sections replace parallel MAC/IP lists. Disabled records
# must remain visible in LuCI without entering the live nft policy.
export MOCK_DEVICE_MAC= MOCK_DEVICE_MACS= MOCK_DEVICE_IPS= MOCK_DEVICE_IPV4= MOCK_DEVICE_IPV6=
export MOCK_DEVICE_SECTIONS='managed1 managed2' MOCK_MANAGED1_MAC='02:00:00:00:00:21' MOCK_MANAGED1_ENABLED=1
export MOCK_MANAGED2_MAC='02:00:00:00:00:22' MOCK_MANAGED2_ENABLED=0
if ! sh "$ctl" apply || ! sh "$ctl" verify; then
    echo "managed device section apply failed" >&2
    exit 1
fi
grep -Fx 02:00:00:00:00:21 "$NFT_STATE/ios_enabled_mac" >/dev/null
! grep -Fx 02:00:00:00:00:22 "$NFT_STATE/ios_enabled_mac" >/dev/null

# Presence monitoring includes valid configured rows even when location
# processing for one row is disabled. Dynamic online elements are reported as
# JSON and expire in the kernel after 30 minutes.
sh "$ctl" sync_monitor
grep -Fx 02:00:00:00:00:21 "$NFT_STATE/ios_monitored_mac" >/dev/null
grep -Fx 02:00:00:00:00:22 "$NFT_STATE/ios_monitored_mac" >/dev/null
printf '%s\n' 02:00:00:00:00:21 > "$NFT_STATE/ios_online_mac"
printf '%s\n' 192.0.2.21 > "$NFT_STATE/ios_online_v4"
online_json=$(sh "$ctl" online)
printf '%s\n' "$online_json" | grep -F '"mac":["02:00:00:00:00:21"]' >/dev/null
printf '%s\n' "$online_json" | grep -F '"ipv4":["192.0.2.21"]' >/dev/null

unset MOCK_DEVICE_SECTIONS MOCK_MANAGED1_MAC MOCK_MANAGED1_ENABLED MOCK_MANAGED2_MAC MOCK_MANAGED2_ENABLED

# Duplicate device elements are rejected before nft is modified, preventing
# a real nft EEXIST from leaving a partially applied policy.
export MOCK_DEVICE_MACS='02:AA:BB:CC:DD:EF 02:AA:BB:CC:DD:EF'
if sh "$ctl" apply; then
    echo "duplicate MAC unexpectedly accepted" >&2
    exit 1
fi
unset MOCK_DEVICE_MACS

export MOCK_DEVICE_IPV4=192.0.2.10 FAIL_ADD_SET=ios_enabled_v4
if sh "$ctl" apply; then
    echo "mid-apply failure unexpectedly succeeded" >&2
    exit 1
fi
for set_name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6; do
    [ ! -s "$NFT_STATE/$set_name" ] || { echo "set $set_name retained elements" >&2; exit 1; }
done
[ ! -e "$LOCSPOOF_STATE_DIR/nft_applied" ]

# Native nft element lookup must accept equivalent textual representations.
# The fake nft stores lower-case MACs and compressed IPv6, while UCI supplies
# upper-case/full-width forms.
unset FAIL_ADD_SET
unset MOCK_DEVICE_IPS
export MOCK_DEVICE_MAC=02:AA:BB:CC:DD:EE MOCK_DEVICE_IPV4= MOCK_DEVICE_IPV6= MOCK_IPV6_BLOCK=0
if ! sh "$ctl" apply || ! sh "$ctl" verify; then
    echo "uppercase MAC native nft get verification failed" >&2
    exit 1
fi
export MOCK_DEVICE_MAC=02:aa:bb:cc:dd:ee
if ! sh "$ctl" verify; then
    echo "lowercase MAC equivalent verification failed" >&2
    exit 1
fi
export MOCK_DEVICE_MAC=02:AA:BB:CC:DD:EE
export MOCK_DEVICE_IPV6=2001:0db8:0000:0000:0000:0000:0000:0001
if ! sh "$ctl" apply || ! sh "$ctl" verify; then
    echo "expanded IPv6 native nft get verification failed" >&2
    exit 1
fi
export MOCK_DEVICE_IPV6=2001:db8::1
if ! sh "$ctl" verify; then
    echo "compressed IPv6 equivalent verification failed" >&2
    exit 1
fi

# nft JSON may mix scalar and prefix members and append timeout metadata. The
# parser must retain both forms rather than dropping the prefix when a scalar
# value is present.
export MOCK_JSON_PREFIX=1
json_status=$(sh "$ctl" status)
printf '%s\n' "$json_status" | grep '"target_v4_count":[2-9]' >/dev/null
unset MOCK_JSON_PREFIX

# Run against a checked-in OpenWrt-shaped fixture (metainfo, set.elem,
# timeout and prefix metadata), not only generated mock JSON.
export NFT_FIXTURE="$root/tools/fixtures/nft-list-set-real.json"
fixture_status=$(sh "$ctl" status)
printf '%s\n' "$fixture_status" | grep '"target_v4_count":4' >/dev/null
unset NFT_FIXTURE

# Any valid-looking nft object outside the four supported element encodings
# must make verification fail rather than being silently dropped.
bad_range="$tmp/bad-range.json"
printf '%s\n' '{ "nftables": [ { "metainfo": {} }, { "set": { "family": "inet", "table": "fw4", "name": "ios_enabled_v6", "elem": [ { "range": [ "192.0.2.10", "192.0.2.20" ] } ] } } ] }' > "$bad_range"
export NFT_FIXTURE="$bad_range" NFT_FIXTURE_SET=ios_enabled_v6
if sh "$ctl" verify; then
    echo "nft range element unexpectedly verified" >&2
    exit 1
fi
bad_object="$tmp/bad-object.json"
printf '%s\n' '{ "nftables": [ { "metainfo": {} }, { "set": { "family": "inet", "table": "fw4", "name": "ios_enabled_v6", "elem": [ { "unknown": { "value": "192.0.2.10" } } ] } } ] }' > "$bad_object"
export NFT_FIXTURE="$bad_object" NFT_FIXTURE_SET=ios_enabled_v6
error_file="$tmp/verify-error.log"
: > "$NFT_LOG"
set +e
sh "$ctl" verify >"$tmp/verify-output.log" 2>"$error_file"
verify_rc=$?
set -e
[ "$verify_rc" -ne 0 ] || { echo "unknown nft element unexpectedly verified" >&2; exit 1; }
! grep -F 'nft target sets are unavailable or empty' "$error_file" >/dev/null || {
	echo 'unknown element test failed before verify_exact_set' >&2
	exit 1
}
grep -F 'list set inet fw4 ios_target_v4' "$NFT_LOG" >/dev/null
grep -F 'list set inet fw4 ios_target_v6' "$NFT_LOG" >/dev/null
grep -F 'list set inet fw4 ios_enabled_v6' "$NFT_LOG" >/dev/null
echo 'unknown element reached verify_exact_set after healthy target verification'
# The production apply path must turn the parser failure into fail-open state,
# not leave a stale applied marker or partially populated device sets.
: > "$LOCSPOOF_STATE_DIR/nft_applied"
if sh "$ctl" apply; then
    echo "apply accepted unknown nft element" >&2
    exit 1
fi
[ ! -e "$LOCSPOOF_STATE_DIR/nft_applied" ]
for name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6; do
    [ ! -s "$NFT_STATE/$name" ] || { echo "parser failure retained $name" >&2; exit 1; }
done
unset NFT_FIXTURE
unset NFT_FIXTURE_SET

# The former hand-written `set.elements` shape must not be accepted as a
# successful element list. It is intentionally reported empty, so a verify
# against a non-empty policy fails instead of silently treating JSON objects
# as members.
old_fixture="$tmp/old-nft.json"
printf '%s\n' '{"nftables":[{"set":{"family":"inet","table":"fw4","name":"ios_enabled_v6","elements":[{"elem":"192.0.2.9"}]}}]}' > "$old_fixture"
export NFT_FIXTURE="$old_fixture"
old_status=$(sh "$ctl" status)
unset NFT_FIXTURE
printf '%s\n' "$old_status" | grep '"target_v4_count":0' >/dev/null || {
	echo "legacy nft JSON was parsed unexpectedly: $old_status" >&2
	exit 1
}

# Exact-set verification rejects stale elements, even when all configured
# members are present. This protects against a removed device remaining live.
printf '%s\n' '192.0.2.250' >> "$NFT_STATE/ios_enabled_v4"
if sh "$ctl" verify; then
    echo "stale extra device element unexpectedly verified" >&2
    exit 1
fi
sed -i '/^192\.0\.2\.250$/d' "$NFT_STATE/ios_enabled_v4"

# Interface values are validated before being interpolated into nft commands.
export MOCK_LAN_INTERFACES='br-lan bad!iface'
if sh "$ctl" apply; then
    echo "invalid LAN interface unexpectedly accepted" >&2
    exit 1
fi
unset MOCK_LAN_INTERFACES

# Missing elements, elements in the wrong set and native nft failures must
# all fail closed rather than being mistaken for a successful verification.
: > "$NFT_STATE/ios_enabled_v6"
if sh "$ctl" verify; then
    echo "missing element unexpectedly verified" >&2
    exit 1
fi
printf '%s\n' '2001:db8::1' > "$NFT_STATE/ios_enabled_v4"
if sh "$ctl" verify; then
    echo "element in wrong set unexpectedly verified" >&2
    exit 1
fi
export FAIL_GET=1
if sh "$ctl" verify; then
    echo "nft get failure unexpectedly verified" >&2
    exit 1
fi

# Active target refresh must use the local dnsmasq resolver and populate both
# address families without waiting for a handset DNS request.
unset FAIL_GET
: > "$NFT_STATE/ios_target_v4"
: > "$NFT_STATE/ios_target_v6"
: > "$NFT_LOG"
: > "$DNS_QUERY_LOG"
unset MOCK_WLOC_HOSTS
export LOCSPOOF_NSLOOKUP="$tmp/bin/nslookup"
if ! sh "$ctl" refresh_targets; then
    echo "target refresh unexpectedly failed" >&2
    exit 1
fi
grep -Fx 203.0.113.5 "$NFT_STATE/ios_target_v4" >/dev/null
grep -Fx 2001:db8::5 "$NFT_STATE/ios_target_v6" >/dev/null
[ "$(wc -l < "$DNS_QUERY_LOG")" -eq 6 ]
grep -Fx gsp-ssl.ls.apple.com "$DNS_QUERY_LOG" >/dev/null
grep -Fx gspe1-ssl.ls.apple.com "$DNS_QUERY_LOG" >/dev/null
grep -Fx gs-loc.apple.com "$DNS_QUERY_LOG" >/dev/null
grep -Fx gs-loc-cn.apple.com "$DNS_QUERY_LOG" >/dev/null
! grep -E 'add element inet fw4 ios_target_v[46]' "$NFT_LOG" >/dev/null
export MOCK_WLOC_HOSTS='gsp-ssl.ls.apple.com gs-loc.apple.com'

# A-only and AAAA-only results are independently valid; target verification
# accepts either family and never requires a synthetic address in the other.
: > "$NFT_STATE/ios_target_v4"; : > "$NFT_STATE/ios_target_v6"
export MOCK_DNS_V4=1 MOCK_DNS_V6=0
sh "$ctl" refresh_targets
unset MOCK_DNS_V4 MOCK_DNS_V6
[ -s "$NFT_STATE/ios_target_v4" ] && [ ! -s "$NFT_STATE/ios_target_v6" ] || {
	echo "A-only target state unexpected: v4=$(cat "$NFT_STATE/ios_target_v4") v6=$(cat "$NFT_STATE/ios_target_v6")" >&2
	exit 1
}
: > "$NFT_STATE/ios_target_v4"; : > "$NFT_STATE/ios_target_v6"
export MOCK_DNS_V4=0 MOCK_DNS_V6=1
sh "$ctl" refresh_targets
unset MOCK_DNS_V4 MOCK_DNS_V6
[ ! -s "$NFT_STATE/ios_target_v4" ] && [ -s "$NFT_STATE/ios_target_v6" ] || {
	echo "AAAA-only target state unexpected: v4=$(cat "$NFT_STATE/ios_target_v4") v6=$(cat "$NFT_STATE/ios_target_v6")" >&2
	exit 1
}

# A failed nft batch preserves the last-known-good targets, but clears device
# interception and the applied marker so stale targets cannot capture traffic.
printf '%s\n' 198.51.100.20 > "$NFT_STATE/ios_target_v4"
export FAIL_BATCH=1
sh "$ctl" refresh_targets && { echo 'batch failure unexpectedly succeeded' >&2; exit 1; }
unset FAIL_BATCH
[ "$(cat "$NFT_STATE/ios_target_v4")" = 198.51.100.20 ]
cat >"$tmp/bin/nslookup" <<'EOF'
#!/bin/sh
sleep 2
printf 'Server: 127.0.0.1\nAddress: 203.0.113.5\n'
EOF
chmod +x "$tmp/bin/nslookup"
export LOCSPOOF_DNS_TIMEOUT_SECONDS=1
sh "$ctl" refresh_targets && { echo 'DNS timeout unexpectedly succeeded' >&2; exit 1; }
unset LOCSPOOF_DNS_TIMEOUT_SECONDS
[ "$(cat "$NFT_STATE/ios_target_v4")" = 198.51.100.20 ] || {
	echo "target set was not preserved after DNS timeout: $(cat "$NFT_STATE/ios_target_v4")" >&2
	exit 1
}

# Every configured host is required to resolve; one failed host aborts the
# entire refresh and preserves the previous target set.
: > "$NFT_STATE/ios_target_v4"; : > "$NFT_STATE/ios_target_v6"
export MOCK_WLOC_HOSTS='gsp-ssl.ls.apple.com gs-loc.apple.com'
cat >"$tmp/bin/nslookup" <<'EOF'
#!/bin/sh
printf '%s\n' "$1" >> "$DNS_QUERY_LOG"
case "$1" in
  gs-loc.apple.com) exit 1 ;;
esac
printf 'Server: 127.0.0.1\nAddress: 203.0.113.5\n'
EOF
chmod +x "$tmp/bin/nslookup"
if sh "$ctl" refresh_targets; then
    echo 'partial DNS failure unexpectedly succeeded' >&2
    exit 1
fi
export MOCK_WLOC_HOSTS='gsp-ssl.ls.apple.com gs-loc.apple.com'
cat >"$tmp/bin/nslookup" <<'EOF'
#!/bin/sh
printf '%s\n' "$1" >> "$DNS_QUERY_LOG"
exit 1
EOF
chmod +x "$tmp/bin/nslookup"
: > "$NFT_STATE/ios_target_v4"; : > "$NFT_STATE/ios_target_v6"
if sh "$ctl" refresh_targets; then
    echo 'all-DNS-failed refresh unexpectedly succeeded' >&2
    exit 1
fi
[ ! -s "$NFT_STATE/ios_target_v4" ] && [ ! -s "$NFT_STATE/ios_target_v6" ]

# State markers are atomically replaced. A symlink at the marker path must be
# replaced, not followed, and the outside sentinel must remain untouched.
outside="$tmp/outside-state"
printf '%s\n' sentinel > "$outside"
if ln -s "$outside" "$LOCSPOOF_STATE_DIR/bypass" 2>/dev/null; then
    if ! sh "$ctl" bypass_on; then
        echo 'bypass marker atomic replacement failed' >&2
        exit 1
    fi
    [ "$(cat "$outside")" = sentinel ]
    [ ! -L "$LOCSPOOF_STATE_DIR/bypass" ]
    [ "$(cat "$LOCSPOOF_STATE_DIR/bypass")" = 1 ]
else
    case "$(uname -s 2>/dev/null || printf unknown)" in
        Linux*|CYGWIN*)
            echo 'symlink test could not create a link on a platform that must support it' >&2
            exit 1 ;;
        *)
            echo 'symlink attack test skipped: symbolic links unavailable on this platform' >&2 ;;
    esac
fi

# Quantify verify's structured JSON parser cost with multiple members in each
# family. A bounded count proves jsonfilter is not spawned once per element.
export MOCK_IPV6_BLOCK=0 MOCK_DEVICE_MAC='' MOCK_DEVICE_MACS='02:00:00:00:00:01 02:00:00:00:00:02 02:00:00:00:00:03' \
    MOCK_DEVICE_IPV4='198.51.100.10' MOCK_DEVICE_IPV6='2001:db8::10' MOCK_DEVICE_IPS='198.51.100.11 198.51.100.12 2001:db8::11 2001:db8::12'
for name in ios_enabled_v4 ios_enabled_v6 ios_enabled_mac ios_enabled_mac_v6 ios_block_v6 ios_block_mac_v6 ios_monitored_v4 ios_monitored_v6 ios_monitored_mac ios_online_v4 ios_online_v6 ios_online_mac ios_lan_ifaces ios_target_v4 ios_target_v6; do
    : > "$NFT_STATE/$name"
done
printf '%s\n' 198.51.100.10 198.51.100.11 198.51.100.12 > "$NFT_STATE/ios_enabled_v4"
printf '%s\n' 2001:db8::10 2001:db8::11 2001:db8::12 > "$NFT_STATE/ios_enabled_v6"
printf '%s\n' 02:00:00:00:00:01 02:00:00:00:00:02 02:00:00:00:00:03 > "$NFT_STATE/ios_enabled_mac"
printf '%s\n' 02:00:00:00:00:01 02:00:00:00:00:02 02:00:00:00:00:03 > "$NFT_STATE/ios_enabled_mac_v6"
printf '%s\n' br-lan > "$NFT_STATE/ios_lan_ifaces"
printf '%s\n' 203.0.113.5 > "$NFT_STATE/ios_target_v4"
printf '%s\n' 2001:db8::5 > "$NFT_STATE/ios_target_v6"
: > "$JSONFILTER_LOG"
 : > "$NFT_LOG"
sh "$ctl" verify
jsonfilter_calls_3=$(wc -l < "$JSONFILTER_LOG" | tr -d ' ')
nft_calls_3=$(wc -l < "$NFT_LOG" | tr -d ' ')

# Repeat with one hundred members. The calls must depend on the nine sets,
# not on the number of expected members.
: > "$NFT_STATE/ios_enabled_mac"; : > "$NFT_STATE/ios_enabled_mac_v6"; : > "$NFT_STATE/ios_enabled_v4"; : > "$NFT_STATE/ios_enabled_v6"
macs=; ips=; ips6=; state_i=1
while [ "$state_i" -le 100 ]; do
    mac=$(printf '02:00:00:00:00:%02x' "$state_i")
    ip="198.51.100.$state_i"
    ip6="2001:db8::$state_i"
    macs="$macs $mac"
    ips="$ips $ip"
    ips6="$ips6 $ip6"
    printf '%s\n' "$mac" >> "$NFT_STATE/ios_enabled_mac"
    printf '%s\n' "$mac" >> "$NFT_STATE/ios_enabled_mac_v6"
    printf '%s\n' "$ip" >> "$NFT_STATE/ios_enabled_v4"
    printf '%s\n' "$ip6" >> "$NFT_STATE/ios_enabled_v6"
    state_i=$((state_i + 1))
done
export MOCK_DEVICE_MAC='' MOCK_DEVICE_MACS="$macs" MOCK_DEVICE_IPV4='' MOCK_DEVICE_IPV6='' MOCK_DEVICE_IPS="$ips $ips6"
: > "$JSONFILTER_LOG"; : > "$NFT_LOG"
sh "$ctl" verify
jsonfilter_calls_100=$(wc -l < "$JSONFILTER_LOG" | tr -d ' ')
nft_calls_100=$(wc -l < "$NFT_LOG" | tr -d ' ')
[ "$jsonfilter_calls_3" = "$jsonfilter_calls_100" ] || {
    echo "jsonfilter calls grew with set members: 3=$jsonfilter_calls_3 100=$jsonfilter_calls_100" >&2
    exit 1
}
[ "$nft_calls_3" = "$nft_calls_100" ] || {
    echo "nft calls grew with set members: 3=$nft_calls_3 100=$nft_calls_100" >&2
    exit 1
}
printf 'verify calls (3 elements): nft=%s jsonfilter=%s\n' "$nft_calls_3" "$jsonfilter_calls_3"
printf 'verify calls (100 elements): nft=%s jsonfilter=%s\n' "$nft_calls_100" "$jsonfilter_calls_100"

echo "locspoofctl clear/MAC-block/transactional-apply mock tests passed"
