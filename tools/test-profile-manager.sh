#!/bin/sh
set -eu

root=${LOCSPOOF_TEST_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
helper="$root/package/ils-gateway/root/usr/bin/locspoof-profile"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/state"
grep -F 'IPKG_INSTROOT=${IPKG_INSTROOT:-}' "$helper" >/dev/null
grep -Fx 'set -e' "$helper" >/dev/null
! grep -Fx 'set -eu' "$helper" >/dev/null
printf '1\n' > "$tmp/state/one"
printf '0\n' > "$tmp/state/two"

cat > "$tmp/functions.sh" <<'EOF'
: "$IPKG_INSTROOT"
config_load() { : "$CONFIG_LIST_STATE"; }
config_foreach() {
	function=$1
	type=$2
	[ "$type" = profile ] || return 0
	"$function" one
	"$function" two
}
config_get() {
	var=$1
	section=$2
	option=$3
	default=${4-}
	value=$default
	[ "$option" = active ] && value=$(cat "$PROFILE_STATE/$section")
	eval "$var=\$value"
}
EOF

cat > "$tmp/bin/uci" <<'EOF'
#!/bin/sh
set -eu
[ "${1-}" = -q ] && shift
case "${1-}" in
	set)
		expr=$2
		section=$(printf '%s' "$expr" | sed -n 's/^[^.]*\.\([^.]*\)\.active=.*/\1/p')
		value=${expr##*=}
		[ -n "$section" ] || exit 1
		printf '%s\n' "$value" > "$PROFILE_STATE/$section" ;;
	commit) : ;;
	*) exit 1 ;;
esac
EOF

cat > "$tmp/bin/service" <<'EOF'
#!/bin/sh
printf '%s\n' "${1-}" >> "$PROFILE_LOG"
[ "${MOCK_SERVICE_FAIL:-0}" = 0 ]
EOF

cat > "$tmp/bin/ctl" <<'EOF'
#!/bin/sh
printf '%s %s\n' "${1-}" "${2-}" >> "$PROFILE_LOG"
EOF
chmod +x "$tmp/bin/"*

export PROFILE_STATE="$tmp/state" PROFILE_LOG="$tmp/log"
export LOCSPOOF_FUNCTIONS="$tmp/functions.sh" LOCSPOOF_SERVICE="$tmp/bin/service" LOCSPOOF_CTL="$tmp/bin/ctl"
export PATH="$tmp/bin:${PATH-}"
unset IPKG_INSTROOT CONFIG_LIST_STATE

sh "$helper" activate two
[ "$(cat "$tmp/state/one")" = 0 ]
[ "$(cat "$tmp/state/two")" = 1 ]
grep -Fx 'reload' "$tmp/log" >/dev/null
! grep -Fx 'refresh all' "$tmp/log" >/dev/null

printf '1\n' > "$tmp/state/one"
printf '0\n' > "$tmp/state/two"
export MOCK_SERVICE_FAIL=1
if sh "$helper" activate two; then
	echo 'failed service reload did not roll back profile' >&2
	exit 1
fi
[ "$(cat "$tmp/state/one")" = 1 ]
[ "$(cat "$tmp/state/two")" = 0 ]

if sh "$helper" activate '../bad'; then
	echo 'invalid profile section was accepted' >&2
	exit 1
fi

echo 'profile activation and rollback tests passed'
