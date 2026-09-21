#!/usr/bin/env bash
# Runtime test for launcher --kill-apps-on-exit on Linux / macOS.
#
# Creates a throwaway sandbox (own apps.json/state/logs, test ports) and verifies:
#   phase 1: with the flag, SIGINT (Ctrl+C) stops the launcher AND every app it started
#   phase 2: with the flag, SIGTERM (what a closed terminal sends) does the same
#   phase 3: without the flag, apps keep running after the launcher exits (default parity)
#
# Usage:  bash kill-on-exit-test.sh [path-to-launcher-binary]
# Needs:  bash, curl, the Go static-server binary, and the launcher binary
#         (launcher-linux-amd64, launcher-darwin-arm64, ... next to this script,
#          or pass an explicit path). Prints ALL_TESTS_PASS on success.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"   # linux | darwin
ARCH="$(uname -m)"
[ "$ARCH" = "x86_64" ] && ARCH=amd64
[ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ] && ARCH=arm64

BIN="${1:-}"
[ -z "$BIN" ] && BIN="$HERE/launcher-$OS-$ARCH"
[ -x "$BIN" ] || BIN="$HERE/launcher-linux"          # fallback: earlier linux build
[ -x "$BIN" ] || { echo "FAIL: no launcher binary found (looked for launcher-$OS-$ARCH)"; exit 1; }
echo "using binary: $BIN ($OS/$ARCH)"
STATIC="$HERE/../StaticServer/static-server-$OS-$ARCH"
[ -x "$STATIC" ] || { echo "FAIL: no static-server binary found at $STATIC"; exit 1; }

PORT_L=9097   # launcher API
PORT_1=8123   # test app 1
PORT_2=8124   # test app 2

SB="$(mktemp -d)"
cleanup() {
  pkill -f "static-server.*$PORT_1" 2>/dev/null
  pkill -f "static-server.*$PORT_2" 2>/dev/null
  rm -rf "$SB"
}
trap cleanup EXIT

mkdir -p "$SB/Launcher"
cat > "$SB/Launcher/apps.json" <<JSON
{
  "launcher_port": $PORT_L,
  "apps": [
    {"id": "srv1", "name": "Srv One", "icon": "1", "desc": "test app one",
     "category": "Test", "dir": ".", "port": $PORT_1,
     "binary": "$STATIC", "args": ["--root", ".", "--port", "{port}", "--host", "127.0.0.1"],
     "cmd": "", "urlPath": "/", "network": false},
    {"id": "srv2", "name": "Srv Two", "icon": "2", "desc": "test app two",
     "category": "Test", "dir": ".", "port": $PORT_2,
     "binary": "$STATIC", "args": ["--root", ".", "--port", "{port}", "--host", "127.0.0.1"],
     "cmd": "", "urlPath": "/", "network": false}
  ]
}
JSON

export LAUNCHER_ROOT="$SB/Launcher" NO_OPEN=1 PORT=$PORT_L HOST=127.0.0.1
wait_api() {
  for _ in $(seq 1 30); do
    curl -s -o /dev/null "http://127.0.0.1:$PORT_L/api/apps" && return 0
    sleep 0.5
  done
  echo "FAIL: launcher API never came up"; exit 1
}
up()   { curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$1/"; }
start_app() { curl -s -XPOST -d "{\"id\":\"$1\"}" "http://127.0.0.1:$PORT_L/api/start" >/dev/null; }

echo "=== PHASE 1: flag ON — SIGINT (Ctrl+C) stops launcher + both apps ==="
"$BIN" --kill-apps-on-exit &
LPID=$!
wait_api
start_app srv1
start_app srv2
sleep 2
up $PORT_1 || { echo "PHASE1 FAIL: srv1 not up"; exit 1; }
up $PORT_2 || { echo "PHASE1 FAIL: srv2 not up"; exit 1; }
echo "  both apps running — sending SIGINT to launcher ($LPID)"
kill -INT $LPID
sleep 3
kill -0 $LPID 2>/dev/null        && { echo "PHASE1 FAIL: launcher still alive"; exit 1; }
up $PORT_1                       && { echo "PHASE1 FAIL: srv1 still listening"; exit 1; }
up $PORT_2                       && { echo "PHASE1 FAIL: srv2 still listening"; exit 1; }
pgrep -f "http.server $PORT_1" >/dev/null && { echo "PHASE1 FAIL: helper child still alive"; exit 1; }
grep -q 'pid' "$SB/Launcher/state.json" 2>/dev/null && { echo "PHASE1 FAIL: state.json still has entries"; exit 1; }
echo "PHASE1_PASS (launcher + both apps stopped, state.json clean)"

echo "=== PHASE 2: flag ON — SIGTERM (terminal closed) stops launcher + app ==="
rm -f "$SB/Launcher/state.json"
"$BIN" --kill-apps-on-exit &
LPID=$!
wait_api
start_app srv1
sleep 2
up $PORT_1 || { echo "PHASE2 FAIL: srv1 not up"; exit 1; }
kill -TERM $LPID
sleep 3
kill -0 $LPID 2>/dev/null && { echo "PHASE2 FAIL: launcher still alive"; exit 1; }
up $PORT_1                && { echo "PHASE2 FAIL: srv1 still up"; exit 1; }
echo "PHASE2_PASS"

echo "=== PHASE 3: flag OFF — app must SURVIVE the launcher's SIGINT (parity) ==="
rm -f "$SB/Launcher/state.json"
"$BIN" &
LPID=$!
wait_api
start_app srv1
sleep 2
up $PORT_1 || { echo "PHASE3 FAIL: srv1 not up"; exit 1; }
kill -INT $LPID
sleep 3
kill -0 $LPID 2>/dev/null && { echo "PHASE3 FAIL: launcher still alive"; exit 1; }
up $PORT_1 || { echo "PHASE3 FAIL: srv1 died with flag off — keep-running parity broken"; exit 1; }
echo "PHASE3_PASS (app kept running without the flag)"
echo "ALL_TESTS_PASS"
