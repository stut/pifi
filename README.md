# PiFi

PiFi gets a headless Raspberry Pi onto WiFi. When the Pi has no connectivity
it raises a WiFi hotspot with a captive portal; you join the hotspot from a
phone or laptop, enter the credentials of the network the Pi should use, and
PiFi takes the hotspot down and connects.

It runs as a systemd service and must run as root.

The hotspot is password-protected by default. Anyone with that password can
configure the device. Read [Security](SECURITY.md), including the upgrade notes
for existing installations and display integrations, before deployment.

## Install

From an interactive terminal on the Pi (Raspberry Pi OS Bookworm or later):

```sh
curl -fsSL https://raw.githubusercontent.com/stut/pifi/main/install.sh | sh
```

The [installer](install.sh) selects the latest stable release for your OS,
verifies the archive against GitHub's SHA-256 digest, installs it to
`/usr/local/bin/pifi`, and runs interactive setup. It requires `curl`, Python 3,
NetworkManager, and `sudo` (unless already root). A wireless interface named
`wlan0` must be available. Existing configuration and service files are kept
unless you choose to replace them during setup. The service is not restarted
automatically, so you can record the hotspot password first.

To inspect the installer before running it, download it to a file:

```sh
curl -fsSL https://raw.githubusercontent.com/stut/pifi/main/install.sh -o install.sh
less install.sh
sh install.sh
```

Alternatively, download a release archive (or [build one](#building)), extract
it, copy the binary to the Pi and run (use `armv6` for any 32-bit OS, `arm64` for a
64-bit OS):

```sh
sudo ./pifi setup
```

Setup is interactive. It:

- checks for a wireless interface;
- checks NetworkManager is running;
- offers to copy the binary to `/usr/local/bin/pifi`;
- writes a root-only config to `/etc/pifi.yaml` with a generated hotspot password;
- prints that password for you to save before disconnecting from the Pi;
- installs and enables `/etc/systemd/system/pifi.service`.

Then:

```sh
sudo systemctl start pifi
journalctl -u pifi -f
```

## Usage

```
pifi [-c config.yaml] [-v]
pifi setup
```

`-c` is the config file path (default `/etc/pifi.yaml`); `-v` prints the
version. PiFi exits non-zero if it is not root, has no config file, or the
configured wireless interface does not exist.

## Behaviour

1. On startup PiFi tears down any hotspot left over from a previous run and
   gives the OS a chance to join a known WiFi network first.
2. If there is still no connectivity once the startup grace period (30
   seconds) has passed, it raises the hotspot and starts the captive portal
   on port 80.
3. Submitting WiFi credentials in the portal stops the hotspot and connects
   to that network.
4. Connectivity is re-checked every `connectivity_check_interval`. The
   hotspot is stopped when connectivity returns and raised again when it is
   lost.
5. While the hotspot is up and nobody has used
   the portal for a couple of minutes, PiFi periodically drops the hotspot
   briefly to see whether a known network has come back into range.
6. Stopping the service (`systemctl stop pifi`) takes the hotspot down.

## Requirements

Raspberry Pi OS Bookworm or later (anything running NetworkManager). PiFi
drives it through `nmcli` and needs no other packages.

## Configuration

`/etc/pifi.yaml`. Every key is optional; a missing key, an empty section or
an empty file falls back to the defaults shown. Durations use Go syntax
(`30s`, `1m`).

```yaml
debug: false     # debug logging
jsonlog: false   # log as JSON instead of text

hotspot:
  # How often to check connectivity and start/stop the hotspot accordingly
  connectivity_check_interval: 1m

  connectivity_check:
    method: ping          # "ping" or "http"
    config: "1.1.1.1"     # host/IP to ping, or URL for http
    timeout: 5s           # per attempt
    retry_count: 3        # attempts before connectivity is considered lost
    retry_interval: 10s   # delay between attempts
    status_filename: /run/pifi-connectivity-status.json
    status_file_mode: "0600" # root only; "0640" for a display group, "0644" for all users

  ssid: "PiFi-*"          # a '*' is replaced with 6 random characters
  password: "*"           # '*' = 16 new random characters on each service start;
                          # setup saves a fixed generated password instead
                          # empty explicitly enables an insecure open network
  channel: 7
  wireless_interface: wlan0

  # Shell commands run when the hotspot starts / stops, e.g. to stop and
  # start services that need internet access
  on_start_hotspot: ""
  on_stop_hotspot: ""
  command_timeout: 5s           # per attempt of the commands above
  command_retry_attempts: 3
  command_retry_interval: 10s

captiveportal:
  listen_address: "10.42.0.1:80" # hotspot address only
  redirect_url: "https://duckduckgo.com/"  # where to send the user once online
  templates_dir: ""       # custom HTML templates (empty = built-in)
  cache_templates: true
  static_dir: ""          # custom static files (empty = built-in)
```

## Status file

PiFi writes its current state to `/run/pifi-connectivity-status.json`
(`hotspot.connectivity_check.status_filename`) after every connectivity check
and before each reconnect probe. Other apps on the device, such as a display,
can watch this file. It is a public interface: its shape is treated as a
stable API (see [ADR-0001](docs/adr/0001-connectivity-status-file-is-a-public-interface.md)).

```json
{
  "timestamp": "2026-01-02T15:04:05.123456789Z",
  "gateway_connected": false,
  "internet_connected": false,
  "hotspot_ssid": "PiFi-a1B2c3",
  "hotspot_password": "",
  "error": ""
}
```

| Field | Meaning |
| --- | --- |
| `timestamp` | When the status was written (RFC 3339) |
| `gateway_connected` | A default gateway is present |
| `internet_connected` | The connectivity check succeeded |
| `hotspot_ssid`, `hotspot_password` | Credentials of PiFi's hotspot, for showing to the user |
| `error` | Error from the last check, or empty |

The file contains the hotspot password and is root-readable by default.
Display services can use a dedicated group and `status_file_mode: "0640"`;
see [Security](SECURITY.md#local-credentials-and-display-integration) for setup.
Updates replace the file atomically: reopen the path to read each update and
watch its parent directory if using filesystem notifications.

## Building

```sh
./build.sh        # build/pifi for linux/arm v6 (runs on every Pi model)
go test -race ./...
```

`./build.sh` embeds `git describe --tags --always --dirty` as the version;
override it with `PIFI_VERSION=v0.2.0 ./build.sh`. A plain `go build` reports
`PiFi dev`. Release binaries report their exact tag, for example `PiFi v0.2.0`.

For a development device, `./build-and-run.sh <ssh-host>` copies the ARMv6
build to the specified host's home directory and restarts PiFi there. The
device's service must already be configured to use that copy of the binary.

## Releases

After merging the intended changes to `main` and checking CI, publish a release:

```sh
gh release create v0.2.0 --repo stut/pifi --target main \
  --title "PiFi v0.2.0" --generate-notes
```

Publishing a release (including publishing a saved draft) triggers the release
workflow. Pushing a tag alone does not. The workflow builds ARMv6 and ARM64
archives with the tag embedded in `pifi -v`, includes the license and docs, and
uploads SHA-256 checksum files. Wait for both build jobs to finish before
directing users to the installer; it refuses a release whose required asset
is still missing. GitHub's latest-release endpoint excludes prereleases.

## License

[MIT](LICENSE).
