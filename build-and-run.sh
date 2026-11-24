#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ] || [[ "$1" == -* ]]; then
    echo "Usage: $0 <ssh-host>" >&2
    exit 2
fi
target_host=$1

./build.sh
ssh "$target_host" -- sudo systemctl stop pifi
scp build/pifi "$target_host:"
ssh "$target_host" -- sudo systemctl restart pifi
