#!/bin/sh
# Capture two envelopes from a real SDK, one per run.
# usage: capture.sh <language> <name> <image> <command...>
# The program in <language>/src reads its DSN from FAULTLINE_CAPTURE_DSN.
set -eu
language=$1
name=$2
image=$3
shift 3
root=$(cd "$(dirname "$0")" && pwd)
port=${CAPTURE_PORT:-18081}

listener=
bindir=$(mktemp -d)
trap 'kill "$listener" 2>/dev/null || true; rm -rf "$bindir"' EXIT
go build -o "$bindir/capture" "$root/capture"

for run in 1 2; do
  "$bindir/capture" -addr ":$port" -out "$root/$language/$name.run$run.envelope" &
  listener=$!
  waited=0
  until nc -z localhost "$port" 2>/dev/null; do
    kill -0 "$listener" 2>/dev/null || { echo "capture listener exited early" >&2; exit 1; }
    waited=$((waited + 1))
    if [ "$waited" -ge 150 ]; then
      echo "capture listener did not open port $port within 30s" >&2
      exit 1
    fi
    sleep 0.2
  done
  docker run --rm \
    --add-host=host.docker.internal:host-gateway \
    -v "$root/$language/src:/work" -w /work \
    -e "FAULTLINE_CAPTURE_DSN=http://fixturekey@host.docker.internal:$port/1" \
    "$image" "$@"
  wait "$listener"
done
