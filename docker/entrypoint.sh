#!/bin/sh
# Runtime-only container: builds from the host-mounted source into a
# host-mounted bin dir, then execs the host-persisted binary.
set -e

BIN="${BIN_PATH:-/hostbin/gig}"

cd /app
go build -trimpath -ldflags="-s -w" -o "$BIN" .

exec "$BIN" "$@"
