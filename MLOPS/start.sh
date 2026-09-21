#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")"
PORT="${PORT:-8137}"
exec go run ../StaticServer --root . --port "$PORT" --host 0.0.0.0
