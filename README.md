# iLS Gateway

English | [简体中文](README.zh-CN.md)

Experimental OpenWrt gateway for authorized LAN devices. The data plane has:

- transparent TCP accept routing with Linux `SO_ORIGINAL_DST` and IPv6 support;
- bounded ClientHello buffering, SNI/port policy, TLS MITM for compiled leaf hosts;
- byte-for-byte passthrough for non-target, malformed, oversized, or no-SNI TLS;
- HTTPS/WLoc response rewriting, SOCKS5 upstream transport, and resource limits;
- root-only CA/leaf provisioning via `locspoof-ca`;
- fw4 nft sets, local-DNS target refresh with atomic nft replacement, procd fail-open watchdog, and LuCI UCI form.

The package is restricted to the compiled WLoc hosts and must be used only on
networks and devices owned or explicitly authorized by the operator.

WLoc target addresses are resolved through the router's local DNS listener
(`127.0.0.1`) and replaced atomically in fw4 target sets. The package does not
install temporary dnsmasq includes or depend on dnsmasq nftset side effects.

The transparent listener is fixed at `0.0.0.0:10443` with a matching IPv6
listener, and the LAN CA enrollment endpoint is fixed at `0.0.0.0:10445`.
These ports are intentionally not editable through UCI or LuCI because the
fw4 ruleset is static.

The LuCI interface supports English and Simplified Chinese. It follows LuCI's
configured language or, when set to automatic, the browser's preferred
language.

## Build and Test

```powershell
gofmt -w cmd internal
go test -count=1 ./...
go vet ./...
go test -race ./...
sh tools/test-locspoofctl.sh
sh tools/test-locspoof-watchdog.sh
sh tools/test-init-static.sh
# Run on Linux as root to verify the real UID/GID PKI boundary.
sh tools/test-pki-permissions.sh
.\tools\build.ps1 -Arch amd64
.\tools\build.ps1 -Arch arm64
```

Run both fuzz targets for at least 30 seconds each. Race testing requires a
working C compiler. The OpenWrt package uses the official
`feeds/packages/lang/golang/golang-package.mk` interface and must be built from
a Linux OpenWrt SDK environment; Windows cannot execute the SDK's ELF tools.
`max_buffered_bytes` is a managed per-request buffer budget: the daemon reserves
space for the worst case compressed, decoded, rewritten, and recompressed body
before handling a response. It is not a cap on total process RSS or allocations
made by an injected rewrite callback.

Build the x86_64 OpenWrt IPK on a Linux host with Docker:

```bash
bash tools/build-openwrt-ipk.sh
```

The script keeps the reusable SDK and output outside the Git worktree under
`${XDG_CACHE_HOME:-$HOME/.cache}/ils-gateway-build`. Set `ILS_BUILD_HOME` to
choose another persistent build directory.
The script verifies the official SDK archive checksum and checks out pinned
OpenWrt, packages, and LuCI revisions. If an older cached SDK uses different
feed revisions, choose a fresh build directory or explicitly set
`ILS_REBUILD_SDK=1` for a one-time rebuild.

## Install and Operate

See [Installation](docs/INSTALLATION.md) for supported targets, IPK
installation, first-time configuration, certificate enrollment, upgrades, and
uninstallation. New installations display `iLS Gateway` as the configuration
profile signer. Existing valid signing identities from releases through v41
are preserved during upgrades so deployed trust material is not rotated.

## CA Lifecycle

`locspoof-ca` creates a persistent CA and fixed-host leaf bundles atomically.
The CA private key is `root:root` mode `0600`; `locspoofd` receives only the CA
certificate and leaf bundles. `/ca.pem` and `/fingerprint` are available on
the LAN enrollment listener at port 10445. Removing the package does not remove
the CA from phones; remove the profile on every authorized device first.

## OpenWrt Lifecycle

The service clears nft sets before startup, waits for the control/ready markers,
then applies device sets. Any startup failure, missing leaf, daemon failure,
watchdog health failure, stop, reload, or disable clears the sets. `bypass_on`
creates a marker consumed by the root watchdog; `bypass_off` reapplies the sets.

Install or upgrade with the package manager, then configure the `main` and
`profile` UCI sections before enabling the service. Stop and disable the
service before uninstalling; the package stop hook clears all IPv4, IPv6, MAC,
and temporary nft state. Remove the CA trust profile from each device before
rotating or uninstalling the package.

The device must be visible on the router's LAN bridge (`br-lan`) so nftables
can match its MAC and current addresses. A downstream NAT/router hides the
phone's MAC and cannot be safely auto-enabled. Configure an explicit MAC and
validated IP constraints; the service refuses invalid or incomplete profiles.
Selectors are OR-matched: MAC rules match Ethernet frames and IP rules match
packets. MAC+IP configuration is therefore an additional constraint set, not
a joined identity assertion. The `lan_interface` list defaults to `br-lan` and
is validated before being loaded into the nft interface set.

## External Validation

This repository does not claim iOS trust, WLoc compatibility, or Core Location
consumption. A real authorized iPhone/iPad and OpenWrt router must record
`response_modified`, `core_location_changed`, and `app_consumed` separately.
See the [validation guide](docs/VALIDATION.md) for the evidence checklist.

## Privacy and Security

The service has no telemetry. Runtime policy, certificates, and successful
rewrite activity remain on the router. The optional altitude-completion action
sends the entered latitude and longitude to the Open-Meteo Elevation API only
when the operator clicks the action. See [Privacy](docs/PRIVACY.md) and
[Security Policy](SECURITY.md) before enabling interception.

## Contributing and Licensing

Contributions are described in [CONTRIBUTING.md](CONTRIBUTING.md). Original
project code is MIT licensed. Bundled browser assets retain their upstream
licenses; see [Third-Party Notices](THIRD_PARTY_NOTICES.md).
