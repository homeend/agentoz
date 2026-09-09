#!/bin/sh
# Runs the test suite on Linux, WSL, or macOS. Windows users: test.cmd.
#
#   ./test.sh              all packages
#   ./test.sh -race        with the race detector (server package matters most)
#   ./test.sh -live        also the opt-in tmux test (needs a running tmux server)
#   ./test.sh -cross       also type-check every package and test for windows/amd64
#   ./test.sh -v ./internal/screen/   verbose, only these packages
#
# Flags combine. Packages default to ./... . -count=1 always: cached
# results hide flakes in the goroutine-heavy server package.
set -e
cd "$(dirname "$0")"

race=""; verbose=""; cross=0; pkgs=""
for a in "$@"; do
  case "$a" in
    -race) race="-race" ;;
    -live) export ERBRUS_TMUX_TEST=1 ;;
    -cross) cross=1 ;;
    -v) verbose="-v" ;;
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    *) pkgs="$pkgs $a" ;;
  esac
done
[ -n "$pkgs" ] || pkgs="./..."

gofmt_out=$(gofmt -l cmd internal)
if [ -n "$gofmt_out" ]; then
  echo "gofmt: these files are not formatted:" >&2
  echo "$gofmt_out" >&2
  exit 1
fi

go vet $pkgs
# shellcheck disable=SC2086
go test -count=1 $race $verbose $pkgs

if [ "$cross" = 1 ]; then
  echo "--- windows/amd64 type-check (tests included)"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet $pkgs
fi
echo "OK"
