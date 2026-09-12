# Changelog

All notable user-facing changes are documented here.

## 42 - 2026-09-13

- Prepare the repository for public collaboration and reproducible builds.
- Use a generic `iLS Gateway` profile signer for new installations while
  preserving valid legacy signing identities during upgrades.
- Pin OpenWrt feed revisions and verify the official SDK archive checksum.
- Include bundled QRCode.js and Lucide license notices in source and IPK output.
- Adopt the canonical GitHub Go module path.
- Add installation, privacy, security, contribution, and CI documentation.

## 41 - 2026-08-17

- Add Apple-required TLS server-authentication EKU to generated endpoint leaves.
- Preserve valid endpoint certificates across service restarts.

## 39

- Bundle `locspoof-timeout` and remove the unavailable external timeout package.

## 37

- Rename the OpenWrt package and IPK prefix to `ils-gateway` while retaining
  compatibility runtime identifiers.
