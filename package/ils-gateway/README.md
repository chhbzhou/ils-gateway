# iLS Gateway

The OpenWrt package ID and IPK filename prefix are `ils-gateway`. The legacy
runtime identifiers `/etc/config/ios-location-spoofer`,
`/etc/init.d/ios-location-spoofer`, and the matching LuCI resource paths are
retained only to preserve existing router configuration and upgrades. They are
implementation details, not the project or package name.

This directory contains the complete OpenWrt integration and builds the Go
daemon, CA helper, and control health client through the official OpenWrt Go
package infrastructure.

The LuCI interface ships English and Simplified Chinese text in the same IPK.
It follows the configured LuCI language or the browser preference when LuCI is
set to automatic language selection.

The package declares all external runtime tools it invokes: `firewall4`,
`nftables-json`, `jsonfilter`, `openssl-util`, `rpcd`, `rpcd-mod-ucode`,
`coreutils-stat`, `shadow-su`, `ca-bundle`, and `luci-base`. DNS lookups use the
bundled `locspoof-timeout` helper, so installation does not depend on the
optional `coreutils-timeout` feed package. It does not depend on `dnsmasq-full`
or nftset side effects; WLoc target addresses are resolved through the local
resolver and installed transactionally.

* `root/etc/config/ios-location-spoofer` provides disabled-by-default UCI
  settings. Structured device records carry a name, note, enabled state and
  MAC/IP selector. Structured location profiles carry a name, note and one
  active marker. Legacy selectors and the original `location` profile are
  migrated without dropping the existing policy.
* `root/etc/init.d/ios-location-spoofer` manages the daemon with procd and
  refreshes WLoc target addresses through the local resolver on start. Stop, crash recovery, bypass,
  and uninstall clear all device nft sets.
* `root/etc/nftables.d/30-ios-location-spoofer.nft` rejects direct LAN access
  to the transparent port while allowing conntrack-marked REDIRECT traffic.
  The enrollment listener is a separate LAN-scoped service and must be
  restricted by the firewall input zone. The daemon must add device-specific
  interception rules only after runtime validation and remove them at stop.
* WLoc addresses are resolved through the router's local DNS listener
  (`127.0.0.1`) and atomically replaced in nftables; no temporary dnsmasq
  include or nftset side effect is installed.
  A successful refresh is recorded under the root-owned state directory
  (`/var/lib/locspoofd`) and repeated
  every five minutes (or immediately when a required address family expires).
  The compiled host allowlist covers the observed iOS endpoint
  `gspe1-ssl.ls.apple.com`, the current `gs-loc.apple.com` and
  `gs-loc-cn.apple.com` endpoints, the legacy `gsp-ssl.ls.apple.com`
  endpoint, and the AutoNavi aliases `bluedot.is.autonavi.com` and
  `bluedot.is.autonavi.com.gds.alibabadns.com`. Package upgrades append newly
  supported hosts without replacing the existing device configuration.
* Device selectors are OR-matched by nft: a configured MAC matches frames and
  a configured IP matches packets. Supplying both is not a cryptographic
  identity join; MAC-only and IP-only policies are valid and must be used with
  the documented LAN visibility/topology constraints.
* LuCI commits modal edits and row actions immediately. Generic Save and Save &
  Apply controls are hidden, Reset remains available, and destructive or
  service-wide actions require confirmation.
* Profile, device, IPv6 policy, LAN-interface, and WLoc-host edits reload the
  active policy through the control socket without replacing the daemon
  process. Enabling or disabling the service starts or stops it. Listener,
  SOCKS5, connection-limit, and buffer-limit changes still require a process
  restart because they own process-level resources.
* Successful WLoc rewrites are recorded by client IP and, when ARP resolution
  is available, by MAC under `/var/lib/locspoofd-activity/activity.json`.
  The `0600` history survives service and router restarts. The device online
  label remains based on the existing 30-minute nft traffic tracker; DHCP host
  hints are used only as a best-effort device-type label.
* Location profiles support a random radius applied independently to each WLoc
  response. The LuCI editor generates non-zero horizontal and vertical
  accuracy defaults when those values are missing or zero. Its adjacent
  altitude action validates both coordinates before querying the fixed
  Open-Meteo Elevation endpoint through a bounded helper request.
* LAN interface names default to `br-lan`, are validated before nft updates,
  and are applied to a dedicated `ios_lan_ifaces` set used by redirect and CA
  input rules.
* `root/usr/share/rpcd/acl.d/...json` grants LuCI access only to the relevant
  UCI package, service listing, and whitelisted helper actions.

At runtime the init script verifies that the fw4-owned device sets exist and
reloads fw4 once when a fresh install has not loaded the include yet. The
root watchdog rechecks the sets against UCI after every firewall reload and
reapplies them only when the daemon is healthy and bypass is off. A failed
reload or verification leaves all device sets empty (fail-open). The package
does not reload firewall services while `IPKG_INSTROOT` is set during an
offline image build.

The init script creates the unprivileged `locspoofd` account and keeps both
private signing keys root-only. The PKI directory is `root:locspoofd` `0750`,
public certificates, the signed profile, and leaf bundles are group-readable
`0640`, and private keys are `root:root` `0600`; startup refuses to launch if
this boundary cannot be verified. Build the package on Linux with an OpenWrt 24.10 SDK;
the SDK's ELF host tools cannot run natively on Windows.

Valid host leaf certificates are preserved across service restarts and package
upgrades. A leaf is regenerated only when it is missing, damaged, expired,
issued by a different CA, does not match its configured hostname, or lacks the
TLS server-authentication EKU required by Apple platforms.

The enrollment listener keeps `/ca.pem` for compatibility, provides a DER
certificate at `/ca.cer`, and serves a CMS-signed `/ca.mobileconfig` using
Apple's profile MIME type. New installations display `iLS Gateway` as the
signer. Existing valid signing identities are preserved across upgrades, and
iPhone Safari can hand the CA directly to profile installation.
