#!/bin/sh
set -eu

root=${LOCSPOOF_TEST_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
asset_dir="$root/package/ils-gateway/root/www/luci-static/resources/view/ios-location-spoofer"
view="$asset_dir/overview.js"
style="$asset_dir/overview.css"
lucide="$asset_dir/lucide.min.js"
qrcode="$asset_dir/qrcode.min.js"
acl="$root/package/ils-gateway/root/usr/share/rpcd/acl.d/luci-app-ios-location-spoofer.json"
config="$root/package/ils-gateway/root/etc/config/ios-location-spoofer"
english_po="$root/package/ils-gateway/po/en/ils-gateway.po"

for file in "$view" "$style" "$lucide" "$qrcode" "$acl" "$config" "$english_po"; do
	[ -s "$file" ] || { echo "missing LuCI asset: $file" >&2; exit 1; }
done

i18n_strings=$(mktemp "${TMPDIR:-/tmp}/ils-i18n.XXXXXX")
trap 'rm -f "$i18n_strings"' EXIT HUP INT TERM
awk '{
	line = $0
	while (match(line, /_\(\047[^\047]*\047\)/)) {
		print substr(line, RSTART + 3, RLENGTH - 5)
		line = substr(line, RSTART + RLENGTH)
	}
}' "$view" | sort -u > "$i18n_strings"

missing=0
while IFS= read -r msgid; do
	grep -Fx "msgid \"$msgid\"" "$english_po" >/dev/null || {
		echo "missing English LuCI translation: $msgid" >&2
		missing=1
	}
done < "$i18n_strings"
[ "$missing" -eq 0 ] || exit 1
awk '
	/^msgid "/ { id = $0; next }
	/^msgstr "/ && id != "msgid \"\"" {
		if ($0 == "msgstr \"\"") exit 1
		id = ""
	}
' "$english_po"
grep -F 'msgstr "Location service is running"' "$english_po" >/dev/null
grep -F 'msgstr "iLS Location Manager"' "$english_po" >/dev/null
! grep -F 'navigator.language' "$view" >/dev/null

grep -F "handleSaveApply: null" "$view" >/dev/null
grep -F "handleSave: null" "$view" >/dev/null
grep -F "handleReset: null" "$view" >/dev/null
grep -F "uci.save().then(function()" "$view" >/dev/null
grep -F "uci.callApply(timeout, true)" "$view" >/dev/null
grep -F "uci.callConfirm()" "$view" >/dev/null
grep -F "loadScript('ils-lucide-script', 'lucide.min.js', 'lucide')" "$view" >/dev/null
grep -F "loadScript('ils-qrcode-script', 'qrcode.min.js', 'QRCode')" "$view" >/dev/null
grep -F "var assetRevision = '39';" "$view" >/dev/null
grep -F "'?v=' + assetRevision" "$view" >/dev/null
grep -F "new window.QRCode" "$view" >/dev/null
grep -F ":10445/ca.mobileconfig" "$view" >/dev/null
grep -F "openSettings" "$view" >/dev/null
grep -F "openProfileEditor" "$view" >/dev/null
grep -F "openDeviceEditor" "$view" >/dev/null
grep -F "openCertificate" "$view" >/dev/null
! grep -F "关闭时所有设备均不生效" "$view" >/dev/null
! grep -F "开启后只处理下方已启用且 MAC/IP 匹配的设备" "$view" >/dev/null
grep -F "class': 'ils-settings-grid ils-settings-grid-single'" "$view" >/dev/null
grep -F "'href': url" "$view" >/dev/null
grep -F "waitForServiceState" "$view" >/dev/null
grep -F "openDiagnostics" "$view" >/dev/null
grep -F "activate_profile" "$view" >/dev/null
grep -F "setOption(sectionId, 'enabled'" "$view" >/dev/null
grep -F "['online']" "$view" >/dev/null
grep -F "['activity']" "$view" >/dev/null
grep -F "var deviceIsOnline" "$view" >/dev/null
grep -F "var lastModified" "$view" >/dev/null
grep -F "var deviceIdentity" "$view" >/dev/null
grep -F "_('最近成功：%s')" "$view" >/dev/null
grep -F "online ? _('在线') : _('离线')" "$view" >/dev/null
grep -F "fs.exec('/usr/bin/locspoof-elevation'" "$view" >/dev/null
grep -F "_('请先填写有效的经纬度')" "$view" >/dev/null
grep -F "setOption(sid, 'random_radius', randomRadius.value)" "$view" >/dev/null
grep -F "saveAndRun('reload')" "$view" >/dev/null
grep -F "_('未知设备')" "$view" >/dev/null
grep -F "var macField = field(_('Wi-Fi MAC'), mac);" "$view" >/dev/null
grep -F "'map-pin-check'" "$view" >/dev/null
grep -F "class': 'ils-workspace'" "$view" >/dev/null
grep -F "class': 'ils-drawer-overlay'" "$view" >/dev/null
grep -F "class': 'ils-certificate-layout'" "$view" >/dev/null
grep -F "max-width: 1480px" "$style" >/dev/null
grep -F "grid-template-columns: minmax(0, 0.95fr) minmax(0, 1.05fr)" "$style" >/dev/null
grep -F ".ils-topbar::after" "$style" >/dev/null
grep -F "@media (min-width: 1280px)" "$style" >/dev/null
grep -F ".ils-modal-overlay" "$style" >/dev/null
grep -F ".ils-profile-meta code" "$style" >/dev/null
grep -F ".ils-switch" "$style" >/dev/null
grep -F ".ils-settings-grid-single" "$style" >/dev/null
grep -F ".ils-toggle-field" "$style" >/dev/null
grep -F "font-size: 14px" "$style" >/dev/null
grep -F "body.ils-location-page footer" "$style" >/dev/null
grep -F "font-size: 15px" "$style" >/dev/null
grep -F ".ils-app a.ils-button-primary:visited" "$style" >/dev/null
grep -F "color: #ffffff !important" "$style" >/dev/null
grep -F "background: transparent !important" "$style" >/dev/null
grep -F "box-shadow: none !important" "$style" >/dev/null
grep -F ".ils-online-label.is-offline" "$style" >/dev/null
grep -F ".ils-device-type" "$style" >/dev/null
grep -F -- "--ils-device-type: #526b84" "$style" >/dev/null
grep -F ".ils-last-modified" "$style" >/dev/null
grep -F ".ils-input-action" "$style" >/dev/null
grep -F ".ils-input-action-button" "$style" >/dev/null
grep -F ".ils-meta-divider-before-modified" "$style" >/dev/null
grep -F "grid-template-columns: max-content 1px minmax(0, 1fr)" "$style" >/dev/null
grep -F "color: var(--ils-muted) !important" "$style" >/dev/null
grep -F "background: var(--ils-surface)" "$style" >/dev/null
! grep -F "overlay.addEventListener('click'" "$view" >/dev/null
! grep -F "window.open(url" "$view" >/dev/null
! grep -F "var enabled = E('input'" "$view" >/dev/null
! grep -F "E('details', { 'class': 'ils-advanced' }" "$view" >/dev/null
! grep -F ".ils-advanced" "$style" >/dev/null
! grep -F "@media (prefers-color-scheme: dark)" "$style" >/dev/null

! grep -F "form.Map" "$view" >/dev/null
! grep -F "form.GridSection" "$view" >/dev/null
! grep -F "cbi-section" "$view" >/dev/null
! grep -F "所有更改已自动保存" "$view" >/dev/null
! grep -F "撤销未应用更改" "$view" >/dev/null
! grep -F "return uci.apply(10)" "$view" >/dev/null
! grep -F "var online = addresses.length > 0" "$view" >/dev/null
! grep -F "暂未在线" "$view" >/dev/null
! grep -F "刷新定位" "$view" >/dev/null
! grep -F "刷新全部" "$view" >/dev/null
! grep -F "runAction('refresh'" "$view" >/dev/null
! grep -E "https?://.*(lucide|qrcode)" "$view" >/dev/null
! grep -E "fetch[[:space:]]*\(" "$view" >/dev/null

grep -F '"/usr/bin/locspoof-status"' "$acl" >/dev/null
grep -F '"/usr/bin/locspoof-service"' "$acl" >/dev/null
grep -F '"/usr/bin/locspoof-elevation"' "$acl" >/dev/null
grep -F '"luci-rpc": [ "getHostHints" ]' "$acl" >/dev/null
if awk '/"read"[[:space:]]*:/,/"write"[[:space:]]*:/' "$acl" | grep -F 'locspoof-service' >/dev/null; then
	echo "read ACL exposes write helper" >&2
	exit 1
fi

grep -F "option name '默认定位点'" "$config" >/dev/null
grep -F "option active '1'" "$config" >/dev/null

echo "LuCI static checks passed"
