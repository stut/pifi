# Captive Portal

This is the web interface for the captive portal. It has a set of templates embedded from the `templates` folder. These
can be overridden by setting the `templates_dir` option in the configuration.

The listener defaults to `10.42.0.1:80`, the hotspot's address. It uses HTTP;
access is controlled by the hotspot password, with no separate portal login.
See [Security](../SECURITY.md) for the trust boundary and upgrade instructions.

For local template development, `go run ./cmd/testcp` serves a demonstration
on `http://127.0.0.1:8080` without configuring WiFi or printing passwords.
