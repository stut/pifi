# ADR-0001: The connectivity status file is a public interface

## Status

Accepted — 2026-05-03

## Context

`hotspot/connectivitycheck` writes `/run/pifi-connectivity-status.json` from
two places:

- `ConnectivityCheck.Check()` after each connectivity probe.
- `Supervisor.ProbeForReconnect()` before stopping the AP for a reconnect probe.

At first glance this looks like a hidden internal seam — pifi communicating
with itself through the filesystem. The captive portal is in-process; an
in-memory channel or atomic snapshot would be the obvious refactor.

## Decision

The status file is a **public interface**, not an internal seam. It is
consumed by separate processes running on the same device — typically the
display app driving the Pimoroni Inky Impression — which watch the file for
changes and render the current connectivity state, including the hotspot SSID
and password when the AP is up.

The file is the contract. Its location (`/run/pifi-connectivity-status.json`
by default, configurable via
`hotspot.connectivity_check.status_filename`), its JSON
shape (`hotspot/connectivitycheck/status.go`), and the moments it is rewritten
are part of the API pifi exposes to the rest of the device.

## Consequences

- The status-file writes are **not** to be replaced with an in-memory
  pub/sub seam. Doing so would silently break every device app that reads
  the file.
- Schema changes to the status file must be treated as breaking-API changes
  for downstream consumers (display apps, status indicators, etc.).
- If pifi gains additional internal observers of connectivity state, those
  observers should subscribe via a new in-process mechanism — they should
  not piggy-back on the file. The file's audience is external.
- Future architecture reviews should not flag this as a hidden seam.
