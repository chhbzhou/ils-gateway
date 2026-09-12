#!/bin/sh
set -eu

# This test intentionally uses real Unix credentials. It is skipped on
# non-Linux hosts or when not run as root; Windows mode bits are not evidence
# of a UID/GID access boundary.
[ "$(uname -s)" = Linux ] || { echo "PKI UID test skipped: Linux required"; exit 0; }
[ "$(id -u)" = 0 ] || { echo "PKI UID test skipped: root required"; exit 0; }

ca_bin=${LOCSPOOF_CA_BIN-}
[ -n "$ca_bin" ] && [ -x "$ca_bin" ] || {
	echo "PKI UID test skipped: set LOCSPOOF_CA_BIN to locspoof-ca" >&2
	exit 0
}

tmp=$(mktemp -d)
user="locspoof-pki-${PPID}-$$"
group="$user"
hosts='gsp-ssl.ls.apple.com gspe1-ssl.ls.apple.com gs-loc.apple.com gs-loc-cn.apple.com bluedot.is.autonavi.com bluedot.is.autonavi.com.gds.alibabadns.com'
cleanup() {
	userdel "$user" >/dev/null 2>&1 || :
	groupdel "$group" >/dev/null 2>&1 || :
	rm -rf "$tmp"
}
trap cleanup EXIT

if ! groupadd "$group"; then
	echo "PKI UID test failed: could not create test group $group" >&2
	exit 1
fi
if ! useradd -M -s /bin/sh -g "$group" "$user"; then
	echo "PKI UID test failed: could not create test user $user" >&2
	exit 1
fi

# mktemp creates a private root-only directory.  Keep it private from other
# users while granting the test group traversal, otherwise a real UID check
# fails before it reaches the PKI directory.
chown root:"$group" "$tmp"
chmod 0710 "$tmp"
mkdir -p "$tmp/pki"
"$ca_bin" -dir "$tmp/pki"
chown root:"$group" "$tmp/pki" "$tmp/pki/ca-cert.pem" "$tmp/pki/profile-signing-cert.pem" "$tmp/pki/ca.mobileconfig"
for host in $hosts; do
	chown root:"$group" "$tmp/pki/$host.pem"
done
chown root:root "$tmp/pki/ca-key.pem" "$tmp/pki/profile-signing-key.pem"
chmod 0750 "$tmp/pki"
chmod 0640 "$tmp/pki/ca-cert.pem"
chmod 0640 "$tmp/pki/profile-signing-cert.pem" "$tmp/pki/ca.mobileconfig"
for host in $hosts; do
	chmod 0640 "$tmp/pki/$host.pem"
done
chmod 0600 "$tmp/pki/ca-key.pem"
chmod 0600 "$tmp/pki/profile-signing-key.pem"
printf '%s\n' 'unrelated secret' >"$tmp/unrelated-secret"
chown root:root "$tmp/unrelated-secret"
chmod 0600 "$tmp/unrelated-secret"

su -s /bin/sh -c "test -x '$tmp' && test -x '$tmp/pki' && ! ls '$tmp' >/dev/null 2>&1 && test -r '$tmp/pki/ca-cert.pem' && test -r '$tmp/pki/ca.mobileconfig' && ! test -r '$tmp/pki/ca-key.pem' && ! test -r '$tmp/pki/profile-signing-key.pem' && ! test -r '$tmp/unrelated-secret'" "$user"
for host in $hosts; do
	su -s /bin/sh -c "test -r '$tmp/pki/$host.pem' && ! test -r '$tmp/pki/ca-key.pem'" "$user"
done
test "$(stat -c '%U:%G' "$tmp")" = "root:$group"
test "$(stat -c '%a' "$tmp")" = 710
test "$(stat -c '%a' "$tmp/pki")" = 750
test "$(stat -c '%U:%G' "$tmp/pki")" = "root:$group"
test "$(stat -c '%a' "$tmp/pki/ca-key.pem")" = 600
test "$(stat -c '%a' "$tmp/pki/ca-cert.pem")" = 640
test "$(stat -c '%a' "$tmp/pki/profile-signing-cert.pem")" = 640
test "$(stat -c '%a' "$tmp/pki/profile-signing-key.pem")" = 600
test "$(stat -c '%a' "$tmp/pki/ca.mobileconfig")" = 640
for host in $hosts; do
	test "$(stat -c '%a' "$tmp/pki/$host.pem")" = 640
done
test "$(stat -c '%U:%G' "$tmp/pki/ca-key.pem")" = root:root
test "$(stat -c '%U:%G' "$tmp/pki/profile-signing-key.pem")" = root:root
test "$(stat -c '%U:%G' "$tmp/pki/ca-cert.pem")" = "root:$group"
test "$(stat -c '%U:%G' "$tmp/pki/profile-signing-cert.pem")" = "root:$group"
test "$(stat -c '%U:%G' "$tmp/pki/ca.mobileconfig")" = "root:$group"
for host in $hosts; do
	test "$(stat -c '%U:%G' "$tmp/pki/$host.pem")" = "root:$group"
done
echo "PKI UID/GID permission test passed"
