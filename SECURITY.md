# Security Policy

## Supported Versions

Security fixes are developed against the latest release. Older releases may be
used for comparison but are not guaranteed to receive backports.

## Reporting a Vulnerability

Do not publish credentials, private keys, device identifiers, precise personal
locations, or exploit details in a public issue. Use the repository host's
private security-reporting channel when available. If no private channel is
available, open a minimal issue requesting a private contact method without
including sensitive details.

Include the affected version, target OpenWrt version and architecture, impact,
reproduction conditions, and whether private signing material may have been
exposed. Allow maintainers reasonable time to investigate before disclosure.

## Operational Boundary

iLS Gateway intentionally performs scoped TLS interception. Keep the LuCI and
enrollment listeners restricted to trusted LAN interfaces, enroll only
authorized devices, protect router administration, and remove device trust
before rotating or deleting the local CA.
