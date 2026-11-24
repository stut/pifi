# Hotspot

This package creates and manages a hotspot on the Raspberry Pi, with the simple task of detecting when there is no valid
internet connection and switching to a hotspot mode. The hotspot is password-protected by default; its captive portal
allows clients with that password to specify new WiFi credentials. See [Security](../SECURITY.md).

## Behaviour

- **Startup always retries WiFi first.** If the hotspot was left enabled (pifi crashed, was killed, or the power was cut
  while it was up) it is stopped on startup, and any leftover hotspot config changes are undone. The OS then gets the
  startup grace period to rejoin a known network before the hotspot is considered again.
- **The captive portal is kept alive with the hotspot.** Each connectivity check with the hotspot up also makes sure the
  portal is running, and starts it if it is not.
- **Hooks.** `on_start_hotspot` runs after the hotspot has actually been started and `on_stop_hotspot` after it has
  actually been stopped. Each is given `command_timeout` to run and is tried up to `command_retry_attempts` times,
  `command_retry_interval` apart, if it fails. A failing hook is logged and never affects the hotspot. Hooks come from
  the root-owned config file and run as root, exactly as written.
- **Connectivity check methods.** `ping` (config is a host or IP) or `http` (config is a URL; any response with a status
  below 400 counts as connected).
- **Hotspot password.** Defaults to `*`, expanded to 16 cryptographically random characters at startup. Setup saves a
  fixed generated password instead. An explicit empty value opts into an insecure open hotspot; other values must be
  8 to 63 characters after expansion. The SSID's `*` still expands to six characters.
