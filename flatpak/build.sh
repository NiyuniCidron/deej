#!/usr/bin/env bash
# Builds deej as a flatpak bundle you can install on any machine with flatpak.
#
#   ./flatpak/build.sh            # writes flatpak/_build/deej.flatpak
#
# Requires flatpak and flatpak-builder, plus the runtime:
#   flatpak install flathub org.freedesktop.Platform//24.08 org.freedesktop.Sdk//24.08
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$HERE")"

APP_ID="io.github.niyunicidron.deej"
MANIFEST="$HERE/$APP_ID.yml"
BUILD_DIR="$HERE/_build"
BUNDLE="$BUILD_DIR/deej.flatpak"

mkdir -p "$BUILD_DIR"

# CGO_ENABLED=0 keeps the binary static, so it doesn't depend on the runtime's glibc and
# the manifest can install it as-is instead of rebuilding Go inside the sandbox
echo "==> building deej"
(
    cd "$ROOT"

    GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    VERSION_TAG="${DEEJ_VERSION_TAG:-$(git describe --tags --always 2>/dev/null || true)}"

    echo "    gitCommit $GIT_COMMIT, versionTag ${VERSION_TAG:-<none>}"

    CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.gitCommit=$GIT_COMMIT -X main.versionTag=$VERSION_TAG -X main.buildType=flatpak" \
        -o "$BUILD_DIR/deej" .
)

echo "==> building flatpak"
flatpak-builder --force-clean --disable-rofiles-fuse \
    --repo="$BUILD_DIR/repo" \
    "$BUILD_DIR/app" "$MANIFEST"

echo "==> exporting bundle"
flatpak build-bundle "$BUILD_DIR/repo" "$BUNDLE" "$APP_ID"

echo
echo "wrote $BUNDLE"
echo "install it with: flatpak install --user $BUNDLE"
