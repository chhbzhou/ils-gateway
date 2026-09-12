#!/bin/sh
set -eu

script_dir=$(dirname "$0")
root=$(cd "$script_dir/.." && pwd)
makefile="$root/package/ils-gateway/Makefile"
readme="$root/package/ils-gateway/README.md"
deps='coreutils-stat shadow-su ca-bundle luci-base firewall4 nftables-json jsonfilter openssl-util rpcd rpcd-mod-ucode'
for dep in $deps; do
	grep -F "+$dep" "$makefile" >/dev/null || {
		echo "missing package dependency: $dep" >&2
		exit 1
	}
done
if grep -F 'dnsmasq-full' "$makefile" >/dev/null 2>&1; then
	echo 'dnsmasq-full must not be a package dependency' >&2
	exit 1
fi
grep -F 'PKG_NAME:=ils-gateway' "$makefile" >/dev/null || {
	echo 'OpenWrt package ID is not ils-gateway' >&2
	exit 1
}
grep -F 'GO_PKG:=github.com/chhbzhou/ils-gateway' "$makefile" >/dev/null
grep -F 'define Package/ils-gateway/conffiles' "$makefile" >/dev/null || {
	echo 'missing package conffiles declaration' >&2
	exit 1
}
grep -Fx '/etc/config/ios-location-spoofer' "$makefile" >/dev/null || {
	echo 'conffiles declaration has an incorrect path' >&2
	exit 1
}
grep -F '$(INSTALL_CONF) ./root/etc/config/ios-location-spoofer $(1)/etc/config/ios-location-spoofer' "$makefile" >/dev/null || {
	echo 'installed UCI config does not match the conffiles path' >&2
	exit 1
}
grep -F 'USERID:=locspoofd:locspoofd' "$makefile" >/dev/null || {
	echo 'package does not declare the locspoofd service user' >&2
	exit 1
}
grep -F 'PROVIDES:=luci-app-ios-location-spoofer' "$makefile" >/dev/null
grep -F 'CONFLICTS:=luci-app-ios-location-spoofer' "$makefile" >/dev/null
grep -F '$(eval $(call BuildPackage,ils-gateway))' "$makefile" >/dev/null
grep -F 'PKG_RELEASE:=42' "$makefile" >/dev/null
! grep -F '+coreutils-timeout' "$makefile" >/dev/null || {
	echo 'coreutils-timeout must not be an external package dependency' >&2
	exit 1
}
grep -F 'github.com/chhbzhou/ils-gateway/cmd/locspoof-timeout' "$makefile" >/dev/null
grep -F 'THIRD_PARTY_NOTICES.md' "$makefile" >/dev/null
grep -F 'LICENSE.qrcodejs' "$makefile" >/dev/null
grep -F 'LICENSE.lucide' "$makefile" >/dev/null
grep -F 'LOCSPOOF_TIMEOUT:-/usr/bin/locspoof-timeout' "$root/package/ils-gateway/root/usr/bin/locspoofctl" >/dev/null
grep -F 'ils-gateway_*.ipk' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'elif [ "$(basename "$(dirname "$project")")" = project ]' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'workspace=${XDG_CACHE_HOME:-$HOME/.cache}' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'build_home=${ILS_BUILD_HOME:-$workspace/ils-gateway-build}' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'build_root="$build_home/openwrt-24.10.7-x86-64"' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'output_dir="$build_home/artifacts/ipk/openwrt-24.10.7-x86_64"' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'sdk_sha256=996d71f9eab7df2e8acb0bb2c9726426f05c10d419e5f9600d59b14d871f2acb' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'packages_commit=40ab75a27a65dd87448b6c179cd7791f8dbe0121' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'luci_commit=0dc3401b4699b7f9211b491aa7897e3cfca8f5fb' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'host_uid=${ILS_HOST_UID:-$(id -u)}' "$root/tools/build-openwrt-ipk.sh" >/dev/null
! grep -F 'chown -R 1000:1000' "$root/tools/build-openwrt-ipk.sh" >/dev/null
! grep -F 'build_root="$project/.tmp/' "$root/tools/build-openwrt-ipk.sh" >/dev/null
! grep -F 'output_dir="$project/.artifacts/' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'cp -a /src/package/ils-gateway package/' "$root/tools/build-openwrt-ipk.sh" >/dev/null
grep -F 'make package/ils-gateway/compile' "$root/tools/build-openwrt-ipk.sh" >/dev/null
! grep -F 'find bin -type f -name "luci-app-ios-location-spoofer_*.ipk"' "$root/tools/build-openwrt-ipk.sh" >/dev/null
! grep -F '95-ios-location-spoofer' "$makefile" >/dev/null
grep -F 'locspoof-profile' "$makefile" >/dev/null
! grep -F 'ios_refresh_' "$root/package/ils-gateway/root/etc/nftables.d/30-ios-location-spoofer.nft" >/dev/null
! grep -F 'chain ios_session_refresh' "$root/package/ils-gateway/root/etc/nftables.d/30-ios-location-spoofer.nft" >/dev/null
grep -F 'set ios_online_mac' "$root/package/ils-gateway/root/etc/nftables.d/30-ios-location-spoofer.nft" >/dev/null
grep -F 'timeout 30m;' "$root/package/ils-gateway/root/etc/nftables.d/30-ios-location-spoofer.nft" >/dev/null
grep -F 'chain ios_device_activity' "$root/package/ils-gateway/root/etc/nftables.d/30-ios-location-spoofer.nft" >/dev/null
grep -F 'update @ios_online_mac' "$root/package/ils-gateway/root/etc/nftables.d/30-ios-location-spoofer.nft" >/dev/null
! grep -F 'refresh)' "$root/package/ils-gateway/root/usr/bin/locspoof-service" >/dev/null
! grep -F 'initial device session refresh' "$root/package/ils-gateway/root/usr/bin/locspoof-watchdog" >/dev/null
grep -F '$(1)/usr/share/nftables.d/table-post' "$makefile" >/dev/null || {
	echo 'firewall4 table include is not installed in the auto-include directory' >&2
	exit 1
}
if grep -F '$(1)/etc/nftables.d' "$makefile" >/dev/null 2>&1; then
	echo 'package still installs nftables rules outside firewall4 auto-include directories' >&2
	exit 1
fi
migration="$root/package/ils-gateway/root/etc/uci-defaults/90-ios-location-spoofer"
grep -F 'config.location=profile' "$migration" >/dev/null || {
	echo 'missing stale profile migration' >&2
	exit 1
}
grep -F 'config.main.latitude' "$migration" >/dev/null || {
	echo 'profile migration does not detect values written into the main section' >&2
	exit 1
}
for selector in device_mac device_ip device_ipv4 device_ipv6 device_macs device_ips; do
	grep -F "$selector" "$migration" >/dev/null || {
		echo "missing device selector migration: $selector" >&2
		exit 1
	}
done
grep -F 'uci -q add "$config" device' "$migration" >/dev/null
grep -F '.active=1' "$migration" >/dev/null
for host in gspe1-ssl.ls.apple.com gs-loc-cn.apple.com; do
	grep -F "$host" "$migration" >/dev/null || {
		echo "missing WLoc host migration: $host" >&2
		exit 1
	}
done
for host in bluedot.is.autonavi.com bluedot.is.autonavi.com.gds.alibabadns.com; do
	grep -F "$host" "$migration" >/dev/null || {
		echo "missing current WLoc host migration: $host" >&2
		exit 1
	}
done
ctl="$root/package/ils-gateway/root/usr/bin/locspoofctl"
grep -Fx 'set -e' "$ctl" >/dev/null || {
	echo 'locspoofctl does not use OpenWrt-compatible errexit mode' >&2
	exit 1
}
if grep -Fx 'set -eu' "$ctl" >/dev/null 2>&1; then
	echo 'locspoofctl enables nounset even though OpenWrt functions.sh is not nounset-safe' >&2
	exit 1
fi
grep -F 'IPKG_INSTROOT=${IPKG_INSTROOT:-}' "$ctl" >/dev/null || {
	echo 'locspoofctl does not initialize IPKG_INSTROOT for OpenWrt functions.sh under set -u' >&2
	exit 1
}
grep -F 'export IPKG_INSTROOT' "$ctl" >/dev/null || {
	echo 'locspoofctl does not export IPKG_INSTROOT for sourced OpenWrt helpers' >&2
	exit 1
}
grep -F 'coreutils-stat' "$readme" >/dev/null
grep -F 'does not' "$readme" >/dev/null
echo 'OpenWrt package dependency checks passed'
