#!/bin/sh
# Build every app for the HOST platform with the binary names the launcher
# expects: <name>-<goos>-<goarch>[.exe] next to each app's go.mod (see
# platformBinaryPath in Launcher/main.go).
#
#   ./build.sh --console    also emit Launcher/launcher-console.exe (dev build)
#
# The app set is DISCOVERED (tools/genembedded scans the project root) —
# adding or removing an app folder needs no edits here.
#
# The launcher itself is built twice on Windows:
#   launcher.exe          GUI build (-tags gui, no console window) — the one
#                         to double-click for daily use
#   launcher-console.exe  normal console build for development
#
# Cross-platform release zips for clients are built by release.sh instead.
set -e
cd "$(dirname "$0")"
ROOT="$(cd .. && pwd)"

if ! command -v go >/dev/null 2>&1; then
    echo "error: Go toolchain (go 1.27+) is required" >&2
    exit 1
fi

GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}
SUFFIX="-$GOOS-$GOARCH"
EXE=""
[ "$GOOS" = "windows" ] && EXE=".exe"
export CGO_ENABLED=0

echo "== host build: $GOOS/$GOARCH =="

# build <dir> <entrypoint> <binary-name>
build() {
    local dir=$1 entry=$2 name=$3
    echo "-- $name$SUFFIX$EXE"
    (cd "$ROOT/$dir" && go build -trimpath -ldflags "-s -w" -o "$name$SUFFIX$EXE" "$entry")
}

# The apps: discovered from the project root via the shared generator.
MANIFEST="$(mktemp)"
go run ./tools/genembedded -root "$ROOT" -apps apps.json \
    -out "$(mktemp)" -manifest "$MANIFEST" >/dev/null

while IFS="$(printf '\t')" read -r kind id folder entry binname assets; do
    [ "$kind" = "go" ] || continue
    build "$folder" "$entry" "$binname"
done < "$MANIFEST"
rm -f "$MANIFEST"

# Launcher: GUI build (window mode; no console on Windows) is the primary one.
echo "-- launcher (gui)$EXE"
if [ "$GOOS" = "windows" ]; then
    (go build -tags gui -trimpath -ldflags "-H windowsgui -s -w" -o "launcher$EXE" .)
else
    (go build -tags gui -trimpath -ldflags "-s -w" -o launcher .)
fi

if [ "${1:-}" = "--console" ]; then
    echo "-- launcher (console)$EXE"
    (go build -trimpath -o "launcher-console$EXE" .)
fi

echo
echo "done. Start with: Launcher/launcher$EXE  →  http://127.0.0.1:9090"
