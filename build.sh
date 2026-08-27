#!/bin/sh
# Builds erbrus for linux/amd64 (bin/erbrus) and windows/amd64
# (bin/erbrus.exe). Pure-Go deps (modernc sqlite), so CGO stays off and
# Windows cross-compiles from anywhere.
set -e
cd "$(dirname "$0")"

export CGO_ENABLED=0

GOOS=linux GOARCH=amd64 go build -trimpath -o bin/erbrus ./cmd/erbrus
GOOS=windows GOARCH=amd64 go build -trimpath -o bin/erbrus.exe ./cmd/erbrus

ls -la bin/erbrus bin/erbrus.exe
