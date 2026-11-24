# Security

PiFi runs as root and can change the device's network connection. Treat access
to its hotspot as permission to configure the device. Use the latest main
branch when reporting a vulnerability.

## Reporting a vulnerability

Use [GitHub's private vulnerability reporting](https://github.com/stut/pifi/security/advisories/new).
Do not include passwords, tokens, or private device details in public issues.

## Hotspot and portal

New installations use a generated 16-character hotspot password. Setup saves
it in `/etc/pifi.yaml` with mode `0600` and prints it for you to record before
disconnecting. Keep that password private. A configured `password: "*"`
instead generates a new password each time the service starts; use a display
integration or read the status file as root to obtain it.

The portal uses HTTP for captive-portal compatibility and listens only on
`10.42.0.1:80` by default. WPA2 protects the wireless link, but this is not
end-to-end HTTPS: clients that know the hotspot password must be trusted.
There is no separate portal login. Browser requests identified as cross-origin
are rejected, but that is not authentication. Do not forward the portal to
another network or expose it to the internet. Binding to the hotspot address
limits the listener; a firewall is still needed if other networks can route
traffic to that address.

An explicit `password: ""` enables an open hotspot. Anyone in radio range can
then connect, read unencrypted traffic including submitted WiFi credentials,
and attempt to reconfigure the device. Use this only for a deliberate,
physically controlled provisioning session.

## Local credentials and display integration

The status file includes the hotspot password, never the destination network's
password. Its default mode is `0600` (root only). Writes replace the file
atomically, so readers must reopen its path and file watchers should watch the
parent directory rather than a particular inode.

For a display running without root, prefer a dedicated group and directory:

```sh
sudo groupadd --system pifi-display
sudo usermod -aG pifi-display DISPLAY_USER
sudo install -d -o root -g pifi-display -m 2750 /run/pifi
```

Replace `DISPLAY_USER` with the display service's account and restart that
service so it gains the group membership. Configure PiFi with:

```yaml
hotspot:
  connectivity_check:
    status_filename: /run/pifi/connectivity-status.json
    status_file_mode: "0640"
```

Update the display to read that path. Make the directory survive reboots with
this entry in `/etc/tmpfiles.d/pifi.conf`:

```text
d /run/pifi 2750 root pifi-display -
```

The setgid directory gives new status files the display group on Linux.
Only root may write to this directory. Setting `status_file_mode: "0644"`
explicitly restores the previous world-readable behavior, including access to
the hotspot password for every local user. Manual chmod/chown changes to the
file do not survive the next status update.

Keep `/etc/pifi.yaml` root-owned and mode `0600`, especially when adding a
fixed password. Its hook commands execute as root. NetworkManager stores the
destination network's derived WPA2 key in its system connection profile; this
key is still a credential and must remain protected. Privileged local users
can inspect credentials used by PiFi and NetworkManager.

## Upgrading from the open-hotspot defaults

Existing configuration files are not automatically rewritten. Set a strong
hotspot password and run `sudo chmod 600 /etc/pifi.yaml`. A password `*`
placeholder now expands to 16 characters instead of six; check prefixed
passwords still fit the 63-character limit.

The next status write changes an existing world-readable status file to
`0600` unless `status_file_mode` explicitly opts into sharing. Migrate display
permissions and file watchers as described above before deploying the update.
The JSON fields and their meanings are unchanged.
