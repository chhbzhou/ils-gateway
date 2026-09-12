#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
project=${ILS_PROJECT_DIR:-$(cd "$script_dir/.." && pwd)}
if [ -n "${ILS_WORKSPACE:-}" ]; then
  workspace=$ILS_WORKSPACE
elif [ "$(basename "$(dirname "$project")")" = project ]; then
  workspace=$(dirname "$(dirname "$project")")
else
  workspace=${XDG_CACHE_HOME:-$HOME/.cache}
fi
build_home=${ILS_BUILD_HOME:-$workspace/ils-gateway-build}
build_root="$build_home/openwrt-24.10.7-x86-64"
output_dir="$build_home/artifacts/ipk/openwrt-24.10.7-x86_64"
sdk_archive=openwrt-sdk-24.10.7-x86-64_gcc-13.3.0_musl.Linux-x86_64.tar.zst
sdk_url="https://downloads.openwrt.org/releases/24.10.7/targets/x86/64/$sdk_archive"
sdk_sha256=996d71f9eab7df2e8acb0bb2c9726426f05c10d419e5f9600d59b14d871f2acb
openwrt_commit=b40dfac0a31695596f7c1f5f1519302ca8237f6e
packages_commit=40ab75a27a65dd87448b6c179cd7791f8dbe0121
luci_commit=0dc3401b4699b7f9211b491aa7897e3cfca8f5fb
builder_image=ils-openwrt-builder:24.10.7
host_uid=${ILS_HOST_UID:-$(id -u)}
host_gid=${ILS_HOST_GID:-$(id -g)}

case "$host_uid:$host_gid" in
  *[!0-9:]*|:*|*:) echo "invalid host UID/GID: $host_uid:$host_gid" >&2; exit 1 ;;
esac

mkdir -p "$build_root" "$output_dir"

docker run --rm \
  --volume "$project:/src:ro" \
  --workdir /src \
  golang:1.22 \
  sh -euxc 'go test -count=1 ./... && go vet ./...'

docker run --rm \
  --volume "$project:/src:ro" \
  alpine:3.22 \
  sh -euxc '
    mkdir -p /tmp/work
    cp -a /src/package /src/tools /tmp/work/
    chmod +x /tmp/work/package/ils-gateway/root/etc/init.d/ios-location-spoofer
    chmod +x /tmp/work/package/ils-gateway/root/etc/uci-defaults/90-ios-location-spoofer
    chmod +x /tmp/work/package/ils-gateway/root/usr/bin/*
    cd /tmp/work
    sh tools/test-locspoofctl.sh
    sh tools/test-locspoof-watchdog.sh
    sh tools/test-init-static.sh
    sh tools/test-init-lifecycle.sh
    sh tools/test-package-static.sh
    sh tools/test-luci-static.sh
    sh tools/test-pki-permissions.sh
    sh tools/test-profile-manager.sh
  '

if ! docker image inspect "$builder_image" >/dev/null 2>&1; then
  docker build --tag "$builder_image" - <<'DOCKERFILE'
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential clang flex bison gawk gettext git libncurses-dev \
      libssl-dev python3 rsync unzip zlib1g-dev file wget ca-certificates zstd \
 && rm -rf /var/lib/apt/lists/*
DOCKERFILE
fi

docker run --rm \
  --volume "$project:/src:ro" \
  --volume "$build_root:/build" \
  --volume "$output_dir:/out" \
  --env SDK_ARCHIVE="$sdk_archive" \
  --env SDK_URL="$sdk_url" \
  --env SDK_SHA256="$sdk_sha256" \
  --env OPENWRT_COMMIT="$openwrt_commit" \
  --env PACKAGES_COMMIT="$packages_commit" \
  --env LUCI_COMMIT="$luci_commit" \
  --env HOST_UID="$host_uid" \
  --env HOST_GID="$host_gid" \
  --env ILS_REBUILD_SDK="${ILS_REBUILD_SDK:-0}" \
  "$builder_image" \
  bash -euxo pipefail -c '
    sdk_matches() {
      [ -f sdk/rules.mk ] \
        && [ "$(git -c safe.directory=/build/sdk/feeds/base -C sdk/feeds/base rev-parse HEAD 2>/dev/null)" = "$OPENWRT_COMMIT" ] \
        && [ "$(git -c safe.directory=/build/sdk/feeds/packages -C sdk/feeds/packages rev-parse HEAD 2>/dev/null)" = "$PACKAGES_COMMIT" ] \
        && [ "$(git -c safe.directory=/build/sdk/feeds/luci -C sdk/feeds/luci rev-parse HEAD 2>/dev/null)" = "$LUCI_COMMIT" ]
    }

    clone_commit() {
      repo=$1
      commit=$2
      destination=$3
      git init -q "$destination"
      git -C "$destination" remote add origin "$repo"
      git -C "$destination" fetch --depth 1 origin "$commit"
      git -C "$destination" checkout -q --detach FETCH_HEAD
      [ "$(git -C "$destination" rev-parse HEAD)" = "$commit" ]
    }

    cd /build
    if [ -f sdk/rules.mk ] && ! sdk_matches; then
      if [ "$ILS_REBUILD_SDK" = 1 ]; then
        rm -rf sdk
      else
        echo "existing SDK feed revisions do not match the pinned build" >&2
        echo "set ILS_REBUILD_SDK=1 once, or use a fresh ILS_BUILD_HOME" >&2
        exit 1
      fi
    fi

    if [ ! -f sdk/rules.mk ]; then
      if [ ! -s "$SDK_ARCHIVE" ] || ! printf "%s  %s\n" "$SDK_SHA256" "$SDK_ARCHIVE" | sha256sum -c -; then
        rm -f "$SDK_ARCHIVE" "$SDK_ARCHIVE.part"
        wget -nv -O "$SDK_ARCHIVE.part" "$SDK_URL"
        printf "%s  %s\n" "$SDK_SHA256" "$SDK_ARCHIVE.part" | sha256sum -c -
        mv "$SDK_ARCHIVE.part" "$SDK_ARCHIVE"
      fi

      rm -rf sdk.new
      mkdir sdk.new
      tar --zstd -xf "$SDK_ARCHIVE" -C sdk.new --strip-components=1
      cd sdk.new

      rm -rf feeds package/feeds
      mkdir feeds
      clone_commit https://github.com/openwrt/openwrt.git "$OPENWRT_COMMIT" feeds/base
      clone_commit https://github.com/openwrt/packages.git "$PACKAGES_COMMIT" feeds/packages
      clone_commit https://github.com/openwrt/luci.git "$LUCI_COMMIT" feeds/luci
      ./scripts/feeds update -i base
      ./scripts/feeds update -i packages
      ./scripts/feeds update -i luci
      ./scripts/feeds install -a -p base
      ./scripts/feeds install -a -p packages
      ./scripts/feeds install -a -p luci
      make defconfig

      cd /build
      mv sdk.new sdk
    fi

    cd sdk
    if [ ! -x staging_dir/hostpkg/bin/go ]; then
      make package/feeds/packages/golang/host/compile -j2 V=s
    fi

    rm -rf package/ils-gateway cmd internal third_party
    cp -a /src/package/ils-gateway package/
    cp -a /src/cmd /src/internal .
    cp -a /src/third_party .
    cp /src/go.mod /src/LICENSE /src/THIRD_PARTY_NOTICES.md .

    make package/ils-gateway/clean NO_DEPS=1
    make package/ils-gateway/compile NO_DEPS=1 -j2 V=s

    rm -f /out/ils-gateway_*.ipk
    find bin -type f -name "ils-gateway_*.ipk" -exec cp -v {} /out/ \;
    test "$(find /out -maxdepth 1 -type f -name "ils-gateway_*.ipk" | wc -l)" -eq 1
    chown -R "$HOST_UID:$HOST_GID" /build /out
  '

find "$output_dir" -maxdepth 1 -type f -name 'ils-gateway_*.ipk' -print
