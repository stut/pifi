#!/bin/sh
# Install the latest stable PiFi release. Requires an interactive terminal.
# Usage: curl -fsSL https://raw.githubusercontent.com/stut/pifi/main/install.sh | sh
set -eu

fail() {
    printf 'PiFi installer: %s\n' "$*" >&2
    exit 1
}

as_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    else
        sudo "$@"
    fi
}

cleanup() {
    if [ -n "$staged_binary" ]; then
        as_root rm -f -- "$staged_binary"
    fi
    if [ -n "$work_dir" ]; then
        rm -rf -- "$work_dir"
    fi
}

download() {
    curl --fail --silent --show-error --location \
        --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 300 --retry 3 \
        --output "$2" "$1"
}

main() {
    work_dir=
    staged_binary=
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    umask 077

    for dependency in curl python3 uname getconf id mktemp install mv rm systemctl nmcli; do
        command -v "$dependency" >/dev/null 2>&1 || fail "Required command not found: $dependency"
    done
    if [ "$(id -u)" -ne 0 ]; then
        command -v sudo >/dev/null 2>&1 || fail "Run as root or install sudo."
    fi
    [ "$(uname -s)" = Linux ] || fail "PiFi requires Linux on a Raspberry Pi."
    # A 64-bit kernel can run a 32-bit OS: select for the userspace bitness.
    case "$(uname -m):$(getconf LONG_BIT)" in
        aarch64:64 | arm64:64) architecture=arm64 ;;
        aarch64:32 | arm64:32 | armv6l:32 | armv7l:32 | armv8l:32) architecture=armv6 ;;
        *) fail "Unsupported architecture; use a 32-bit or 64-bit ARM OS." ;;
    esac

    # stdin contains this script when piped into sh. Setup must read the tty.
    { exec 3<>/dev/tty; } 2>/dev/null || fail "Run from an interactive terminal (SSH: use ssh -t)."
    [ -t 3 ] || fail "An interactive terminal is required for setup."
    systemctl is-active --quiet NetworkManager || fail "NetworkManager must be running (Raspberry Pi OS Bookworm or later)."
    [ "$(nmcli -g GENERAL.TYPE device show wlan0)" = wifi ] || fail "Setup requires a wlan0 wireless interface."

    work_dir=$(mktemp -d)
    printf 'Finding the latest PiFi release for %s...\n' "$architecture"
    download https://api.github.com/repos/stut/pifi/releases/latest "$work_dir/release.json"
    python3 - "$work_dir/release.json" "$architecture" > "$work_dir/asset" <<'PY'
import json
import re
import sys
from urllib.parse import quote

with open(sys.argv[1]) as source:
    release = json.load(source)
tag = release.get("tag_name", "")
if release.get("draft") or release.get("prerelease") or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._+-]*", tag):
    sys.exit("No valid stable release found.")
name = f"pifi-{tag}-linux-{sys.argv[2]}.tar.gz"
assets = [a for a in release.get("assets", []) if a.get("name") == name and a.get("state") == "uploaded"]
if len(assets) != 1:
    sys.exit(f"Release {tag} has no unique {name} asset. Its build may still be running; try again later.")
asset = assets[0]
url = f"https://github.com/stut/pifi/releases/download/{quote(tag, safe='')}/{quote(name, safe='')}"
if asset.get("browser_download_url") != url:
    sys.exit("Unexpected release download URL.")
digest = asset.get("digest") or ""
if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
    sys.exit("The release asset has no SHA-256 digest; refusing an unverified download.")
print(tag)
print(url)
print(digest.removeprefix("sha256:"))
PY
    {
        IFS= read -r version
        IFS= read -r asset_url
        IFS= read -r digest
    } < "$work_dir/asset"

    printf 'Downloading PiFi %s...\n' "$version"
    download "$asset_url" "$work_dir/release.tar.gz"
    python3 - "$work_dir/release.tar.gz" "$digest" "$work_dir/pifi" <<'PY'
import hashlib
import shutil
import sys
import tarfile

with open(sys.argv[1], "rb") as source:
    digest = hashlib.sha256()
    for chunk in iter(lambda: source.read(1024 * 1024), b""):
        digest.update(chunk)
if digest.hexdigest() != sys.argv[2]:
    sys.exit("SHA-256 verification failed; nothing has been installed.")
# Extract only the regular binary, never arbitrary archive paths or symlinks.
with tarfile.open(sys.argv[1], "r:gz") as archive:
    members = [m for m in archive.getmembers() if m.name in ("pifi", "./pifi")]
    if len(members) != 1 or not members[0].isfile():
        sys.exit("The release archive must contain one regular pifi binary.")
    with archive.extractfile(members[0]) as source, open(sys.argv[3], "wb") as target:
        shutil.copyfileobj(source, target)
PY

    printf 'Verified PiFi %s. Installing to /usr/local/bin/pifi...\n' "$version"
    as_root install -d -m 0755 /usr/local/bin
    staged_binary=$(as_root mktemp /usr/local/bin/.pifi-install-XXXXXXXX)
    as_root install -m 0755 "$work_dir/pifi" "$staged_binary"
    # Rename on the same filesystem also works when the old binary is running.
    as_root mv -fT -- "$staged_binary" /usr/local/bin/pifi
    staged_binary=
    as_root /usr/local/bin/pifi setup <&3
    printf '\nSave the hotspot password shown by setup before disconnecting.\n'
    printf 'When ready, start (or restart) PiFi: sudo systemctl restart pifi\n'
}

# Keep execution last so a truncated download cannot run a partial installer.
main "$@"
