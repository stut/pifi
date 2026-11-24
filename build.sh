#!/usr/bin/env bash
set -euo pipefail

version=${PIFI_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || printf dev)}
if [[ ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9.+_-]*$ ]]; then
    echo "Invalid version: use letters, digits, dots, plus signs, underscores or hyphens." >&2
    exit 1
fi

GOOS=linux GOARCH=arm GOARM=6 go build -trimpath -o build/pifi \
    -ldflags="-s -w -X main.version=$version" ./cmd/pifi
