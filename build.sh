#!/usr/bin/env bash
set -e
GOOS=linux GOARCH=arm GOARM=6 go build -trimpath -o build/pifi -ldflags="-s -w" ./cmd/pifi
