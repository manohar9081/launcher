#!/bin/sh
# Assemble the single-file client release: ONE launcher executable per
# platform that carries every app inside it — app binaries, web assets, the
# dashboard, the app registry. Nothing else ships.
#
#   ./release.sh                     all default targets (see TARGETS below)
#   ./release.sh windows/amd64       one target
#   ./release.sh darwin/arm64 linux/amd64   several targets
#
# The app set is DISCOVERED, not maintained here: tools/genembedded scans the
# project root (every folder with a go.mod or an HTML entry page, except
# Launcher/ and StaticServer/) and generates the packaged registry plus a
# build manifest. Deleting an app folder and re-running this script removes
# it from the exe; adding a folder adds it. See README.md.
#
# Output: release/launcher-<os>-<arch>[.exe] and SHA256SUMS.
#
# At runtime the launcher unpacks binaries/assets into the user's data
# directory (%LOCALAPPDATA%\ProjectLauncher on Windows) and hosts the plain
# HTML apps in-process. No files are ever written next to the exe.
set -e
cd "$(dirname "$0")"
PROJECT_ROOT="$(cd .. && pwd)"
OUT="$(pwd)/release"
EMB="$(pwd)/embedded"
mkdir -p "$OUT"

if ! command -v go >/dev/null 2>&1; then
    echo "error: Go toolchain (go 1.27+) is required" >&2
    exit 1
fi

TARGETS=${*:-"windows/amd64 windows/arm64 windows/386 linux/amd64 linux/arm64 linux/arm darwin/amd64 darwin/arm64"}

LDFLAGS="-s -w"

# copy_assets <src-dir> <dest-dir> — runtime files only (html/css/js/data/…).
copy_assets() {
    src=$1; dst=$2
    mkdir -p "$dst"
    tar -C "$src" \
        --exclude='*.go' --exclude='go.mod' --exclude='go.sum' \
        --exclude='start.bat' --exclude='start.sh' --exclude='start.cmd' \
        --exclude='run.sh' --exclude='build.sh' \
        --exclude='README.md' \
        --exclude='*.db' --exclude='*.db-shm' --exclude='*.db-wal' \
        --exclude='*.exe' --exclude='*.log' \
        --exclude='dist' --exclude='logs' --exclude='cmd' --exclude='data_raw' \
        --exclude='state.json' --exclude='state.json.tmp' \
        --exclude='.DS_Store' --exclude='Thumbs.db' \
        -cf - . | tar -C "$dst" -xf -
}

# go build helper: build <os> <arch> <goarm> <dir> <entrypoint> <outfile>
build() {
    local os=$1 arch=$2 goarm=$3 dir=$4 entry=$5 bout=$6
    (cd "$PROJECT_ROOT/$dir" && \
        GOOS=$os GOARCH=$arch GOARM=${goarm:-} CGO_ENABLED=0 \
        go build -trimpath -ldflags "$LDFLAGS" -o "$bout" "$entry")
}

restore_embedded() {
    rm -rf embedded
    mkdir -p embedded
    printf 'Replaced by release.sh at build time.\n' > embedded/.placeholder
}

restore_embedded
trap restore_embedded EXIT

for target in $TARGETS; do
    os=${target%%/*}
    arch=${target##*/}
    goarm=""
    case "$arch" in arm) goarm=7 ;; esac
    exe=""; [ "$os" = "windows" ] && exe=".exe"
    out="launcher-$os-$arch$exe"

    echo "=== $os/$arch ==="

    # ---- discover the app set and stage what gets packed inside the exe ----
    rm -rf "$EMB"
    mkdir -p "$EMB/bin" "$EMB/assets"
    go run ./tools/genembedded -root "$PROJECT_ROOT" -apps apps.json \
        -out "$EMB/apps.json" -manifest "$EMB/manifest.tsv"

    while IFS="$(printf '\t')" read -r kind id folder entry binname assets; do
        [ -n "$kind" ] || continue
        if [ "$kind" = "static" ]; then
            echo "-- assets: $id (hosted in launcher)"
            copy_assets "$PROJECT_ROOT/$folder" "$EMB/assets/$id"
        elif [ "$assets" = "1" ]; then
            echo "-- $binname (binary + assets)"
            build "$os" "$arch" "$goarm" "$folder" "$entry" "$EMB/bin/$binname"
            copy_assets "$PROJECT_ROOT/$folder" "$EMB/assets/$id"
        else
            echo "-- $binname (binary only — assets are embedded in the app)"
            build "$os" "$arch" "$goarm" "$folder" "$entry" "$EMB/bin/$binname"
        fi
    done < "$EMB/manifest.tsv"

    # ---- the single executable ---------------------------------------------
    echo "-- launcher$exe"
    ldflags="$LDFLAGS"
    [ "$os" = "windows" ] && ldflags="-H windowsgui $LDFLAGS"
    (GOOS=$os GOARCH=$arch GOARM=${goarm:-} CGO_ENABLED=0 \
        go build -tags "gui embed" -trimpath -ldflags "$ldflags" -o "$OUT/$out" .)

    # Ad-hoc code signature for macOS (only meaningful when run on macOS).
    if [ "$os" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
        codesign --force -s - "$OUT/$out" 2>/dev/null || true
    fi

    restore_embedded

    ls -lh "$OUT/$out" | awk '{printf "   %s  (%s)\n", $9, $5}'
done

echo
echo "== checksums =="
( cd "$OUT" && for f in launcher-*; do [ -f "$f" ] && sha256sum "$f"; done ) > "$OUT/SHA256SUMS" || true
if ! [ -s "$OUT/SHA256SUMS" ]; then
    ( cd "$OUT" && for f in launcher-*; do [ -f "$f" ] && shasum -a 256 "$f"; done ) > "$OUT/SHA256SUMS"
fi
cat "$OUT/SHA256SUMS"
echo
echo "done. Ship release/launcher-<os>-<arch>.exe — one file, everything inside."
