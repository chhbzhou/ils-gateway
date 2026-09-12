# Privacy

iLS Gateway runs on the operator's router and does not include telemetry,
analytics, advertising, or a hosted control plane.

## Data Stored on the Router

- UCI policy, location profiles, and authorized device selectors.
- A private root CA, profile signing key, and generated endpoint certificates.
- Successful rewrite timestamps associated with client IP addresses and, when
  available from the router ARP table, MAC addresses.
- Runtime health and nftables activity state.

Private keys remain root-only. Successful activity history is stored with mode
`0600`. Operators are responsible for router access control, backups, and
secure disposal.

## External Requests

The optional altitude-completion action contacts the public Open-Meteo
Elevation API. When the operator clicks that action, the entered latitude and
longitude are sent to Open-Meteo to obtain an elevation estimate. The action is
not automatic and is not required for interception.

Normal target-host DNS resolution uses the router's configured local resolver.
The resolver may forward queries according to the operator's DNS configuration.

## Intercepted Traffic

The service handles only compiled and configured Apple WLoc-related hosts for
authorized devices. Non-target or invalid TLS is passed through. WLoc response
bodies are processed in memory and are not logged as raw payloads by default.

Because this project installs a local CA and performs narrowly scoped TLS
interception, use it only on networks and devices you own or administer with
explicit permission.
