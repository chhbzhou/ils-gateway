# Maintainer Guide

This file records the stable project structure and release rules for iLS Gateway.

## Project identity

- Repository and Go module: `github.com/chhbzhou/ils-gateway`
- OpenWrt package ID and artifact prefix: `ils-gateway`
- Compatibility UCI, init, LuCI, and ACL identifiers retain the historical
  `ios-location-spoofer` name so upgrades preserve existing installations.

## Build layout

- Go commands live under `cmd/`; reusable packages live under `internal/`.
- OpenWrt package files live under `package/ils-gateway/`.
- `tools/build-openwrt-ipk.sh` builds the OpenWrt 24.10.7 x86_64 package with
  pinned SDK and feed revisions.
- Build caches and artifacts stay outside the Git worktree. Override their
  location with `ILS_BUILD_HOME`.
- Generated binaries, SDKs, IPKs, coverage files, IDE state, and private
  configuration must never be committed.
- LuCI UI strings use the existing Chinese message IDs in `overview.js` and the
  bundled `po/en/ils-gateway.po` catalog for English. LuCI selects English or
  Simplified Chinese from its configured language or the browser's
  `Accept-Language`; keep both catalogs complete when changing UI copy.

## Runtime boundaries

- `locspoofd` runs as the unprivileged `locspoofd` user.
- TCP `10443` is the fixed transparent WLoc listener.
- TCP `10445` is the fixed LAN-only certificate enrollment listener.
- Root-owned helpers manage fw4/nft state and PKI material.
- The daemon may read public certificates and signed leaf bundles but never CA
  or profile-signing private keys.
- Startup, shutdown, bypass, health failure, target-resolution failure, and
  firewall reload paths must remain fail-open by clearing device interception
  sets before reporting an effective state.

## Persistent and runtime state

- UCI configuration: `/etc/config/ios-location-spoofer`
- PKI: `/etc/locspoof/pki`
- Runtime configuration: `/var/run/locspoofd-config/ios-location-spoofer`
- Daemon socket and ready marker: `/var/run/locspoofd`
- Root-owned network state: `/var/lib/locspoofd`
- Successful rewrite activity: `/var/lib/locspoofd-activity/activity.json`

## Certificate lifecycle

- Preserve an existing valid root CA and profile signer during upgrades.
- Private keys remain `root:root` mode `0600`.
- Daemon-readable certificates and signed bundles are `root:locspoofd` mode
  `0640`; the PKI directory permits group traversal without exposing keys.
- Endpoint leaves must contain matching SANs and the TLS `serverAuth` EKU.
- Repair missing or invalid leaves, but never silently replace a damaged root CA.

## Release checklist

1. Run formatting, unit tests, vet, race tests, both fuzz targets, and every
   `tools/test-*.sh` script.
2. Build amd64 and arm64 Linux binaries.
3. Build the x86_64 IPK with the pinned OpenWrt SDK.
4. Inspect package metadata, conffiles, dependencies, installed permissions,
   third-party notices, and the SHA-256 digest.
5. Tag the exact source commit used for the release and attach the IPK plus its
   checksum.
6. Keep router installation and iOS behavior as explicit external validation;
   software tests alone do not prove `core_location_changed` or `app_consumed`.

## Compatibility limits

- The client must be directly visible on a configured LAN bridge. A downstream
  NAT cannot provide a reliable device MAC identity.
- MAC and IP selectors are OR-matched policy selectors, not a joined identity
  assertion.
- iOS may cache Core Location data after a successful network response rewrite.
- Other OpenWrt architectures require a matching SDK and separate package test.
