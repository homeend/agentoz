#!/bin/sh
# Refreshes the vendored xterm.js under internal/web/static/vendor/.
# Pinned on purpose: bump XTERM_VERSION, run, review the diff, commit.
set -e
cd "$(dirname "$0")/.."
XTERM_VERSION=6.0.0
dst=internal/web/static/vendor
base="https://unpkg.com/@xterm/xterm@$XTERM_VERSION"
mkdir -p "$dst"
curl -fsSL "$base/lib/xterm.js" -o "$dst/xterm.js"
curl -fsSL "$base/css/xterm.css" -o "$dst/xterm.css"
curl -fsSL "$base/LICENSE" -o "$dst/xterm.LICENSE"
printf '@xterm/xterm %s\n' "$XTERM_VERSION" > "$dst/VERSIONS"
ls -la "$dst"
