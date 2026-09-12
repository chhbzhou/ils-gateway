# Contributing

## Development

Use Go 1.22 or newer. Keep runtime behavior fail-open: startup, reload, health,
or policy-validation failures must clear interception selectors rather than
silently intercepting with stale state.

Before submitting a change:

```bash
gofmt -w cmd internal
go test -count=1 ./...
go vet ./...
go test -race ./...
sh tools/test-locspoofctl.sh
sh tools/test-locspoof-watchdog.sh
sh tools/test-init-static.sh
sh tools/test-init-lifecycle.sh
sh tools/test-package-static.sh
sh tools/test-luci-static.sh
sh tools/test-profile-manager.sh
```

Run both fuzz targets for at least 30 seconds when changing ClientHello or WLoc
parsing. Build the full IPK when modifying packaging, init, permissions,
nftables, LuCI assets, or cross-compiled commands.

## Pull Requests

- Keep changes focused and explain observable behavior.
- Add regression tests for parser, lifecycle, permission, or policy changes.
- Do not commit IPKs, SDKs, coverage output, IDE metadata, credentials, private
  keys, real device identifiers, or personal locations.
- Preserve existing UCI and PKI during upgrades unless a documented migration
  explicitly requires otherwise.
- Include upstream attribution and license text for every bundled dependency.
