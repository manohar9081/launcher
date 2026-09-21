#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")"
PORT="${1:-8000}"
exec go run ../StaticServer --root . --port "$PORT" --host 0.0.0.0
