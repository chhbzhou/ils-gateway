# Installation

## Supported Target

The provided build script targets OpenWrt/iStoreOS 24.10.7 on x86_64. Other
architectures require a matching OpenWrt SDK and are not release-tested by the
project.

The router must use firewall4/nftables and expose authorized clients directly
on a configured LAN bridge. A downstream router or NAT device hides client MAC
addresses and prevents reliable MAC-based selection.

## Build the IPK

On a Linux host with Docker:

```bash
git clone https://github.com/chhbzhou/ils-gateway.git
cd ils-gateway
bash tools/build-openwrt-ipk.sh
```

The final path is printed by the script. Set `ILS_BUILD_HOME` to place the SDK,
cache, and artifacts in a persistent directory. The script verifies the SDK
SHA256 and uses pinned OpenWrt, packages, and LuCI commits.

## Install

1. Back up `/etc/config/ios-location-spoofer` and `/etc/locspoof` when upgrading.
2. Upload the architecture-matching `ils-gateway_42_x86_64.ipk` to the router.
3. Refresh normal OpenWrt/iStoreOS package feeds so runtime dependencies are available.
4. Install with `opkg install /tmp/ils-gateway_42_x86_64.ipk` or the LuCI package manager.
5. Open LuCI and configure at least one location and one authorized MAC or IP.
6. Enable the service only after reviewing the generated policy.

The package is disabled by default. It does not bundle external dependency
IPKs; dependencies are resolved from the router's configured feeds.

## Enroll an iPhone or iPad

1. Connect the device to the managed LAN.
2. Use the LuCI certificate action and scan the QR code with the system Camera.
3. Allow Safari to download the configuration profile.
4. Install the profile in Settings.
5. Explicitly trust the installed root CA under Certificate Trust Settings.

Only enroll devices owned by the operator or devices whose owner has provided
explicit authorization. Removing the package does not remove trust from a
device; remove the profile from every enrolled device separately.

## Upgrade

Install the newer IPK through `opkg`. The package preserves the UCI config,
root CA, valid leaf certificates, and valid profile signing identity. Runtime
compatibility paths retain the historical `ios-location-spoofer` name.

After an upgrade, verify:

```sh
/etc/init.d/ios-location-spoofer status
locspoof-status
fw4 check
uci changes
```

## Uninstall

Disable the service first so interception sets are cleared:

```sh
/etc/init.d/ios-location-spoofer disable
/etc/init.d/ios-location-spoofer stop
opkg remove ils-gateway
```

The UCI configuration and PKI may be retained as user data. After making a
backup, remove them manually only when permanent deletion is intended:

```sh
rm -f /etc/config/ios-location-spoofer
rm -rf /etc/locspoof /var/lib/locspoofd /var/lib/locspoofd-activity
```

Finally remove the iLS Gateway profile and CA trust from every enrolled device.
