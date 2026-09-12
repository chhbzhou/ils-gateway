# Validation Guide

Use only an owned or explicitly authorized router and iOS device.

Record for each run:

- device model and iOS/iPadOS version;
- CA fingerprint and whether full trust was enabled;
- WLoc host, SNI, ALPN, HTTP version, path, compression, and sample SHA-256;
- `response_modified` (network evidence);
- `core_location_changed` (OS-level observation);
- `app_consumed` (target application observation).

Required cases:

1. Whitelisted SNI over IPv4 and IPv6 reaches MITM and upstream HTTPS.
2. Non-target SNI, no SNI, malformed, fragmented, and oversized ClientHello
   remains byte-for-byte end-to-end TLS passthrough.
3. Daemon stop/crash, watchdog failure, disable, reload, and uninstall clear
   enabled nft sets and restore normal connectivity.
4. CA download/fingerprint match, certificate trust probe, WLoc rewrite, and
   compressed/uncompressed response behavior.

No real-device result is implied by software tests.

Collect software rewrite evidence from the control socket after a controlled
WLoc request (the response body is never logged):

```sh
/usr/bin/locspoof-health /var/run/locspoofd/control.sock
# inspect result.target_requests, result.response_modified,
# result.response_passthrough, result.last_input_sha256 and
# result.last_output_sha256
```

`response_modified` is incremented only after the rewritten bytes have been
written to the client. The SHA-256 fields are digests, not payload captures;
pair them with the upstream/client capture hash in the test report.

Capture the authoritative state at each checkpoint on the authorized router:

```sh
date -Iseconds
/usr/bin/locspoof-status status > /tmp/ils-status.json
/usr/bin/locspoof-health /var/run/locspoofd/control.sock > /tmp/ils-health.json
cat /tmp/ils-status.json /tmp/ils-health.json
```

`status.effective_enabled` is the sole network-effective verdict. The health
response reports daemon/data-plane health and counters; process health alone
is not evidence that nft redirect rules are active. For each controlled WLoc
request, record the before/after delta of `response_modified`, plus
`last_rewrite_time`, `last_envelope`, `last_locations_modified`, and the input
and output SHA-256 digests. Record these independent iOS observations too:

- `core_location_changed`: OS-level location observation with timestamp;
- `app_consumed`: target app result and version with timestamp.

A modified response does not by itself prove Core Location changed or that an
app consumed the result.
