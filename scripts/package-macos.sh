#!/bin/bash
# Build the desktop client and package a drag-to-Applications disk image.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD=true
API_URL="http://localhost:8080"
OUT="$ROOT/dist"

usage() {
  cat <<'EOF'
Usage: scripts/package-macos.sh [options]
  --control-plane-url URL  Compile a different API address into the app
  --output-dir PATH       Output directory (default: dist)
  --skip-build            Package the existing Release app without rebuilding
  --help                  Show this help

This packages the desktop client only. It does not install backend services.
Developer ID signing and notarization are separate distribution steps.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --control-plane-url)
      [ "$#" -ge 2 ] || { usage >&2; exit 1; }
      API_URL="$2"; shift 2 ;;
    --output-dir)
      [ "$#" -ge 2 ] || { usage >&2; exit 1; }
      OUT="$2"; shift 2 ;;
    --skip-build) BUILD=false; shift ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; usage >&2; exit 1 ;;
  esac
done

[ "$(uname -s)" = Darwin ] || { echo 'Packaging requires macOS.' >&2; exit 1; }
if [ "$BUILD" = false ] && [ "$API_URL" != 'http://localhost:8080' ]; then
  echo '--control-plane-url requires a rebuild; remove --skip-build.' >&2
  exit 1
fi

if [ "$BUILD" = true ]; then
  (cd "$ROOT/app" && flutter build macos --release "--dart-define=CONTROL_PLANE_URL=$API_URL")
fi

APP="$ROOT/app/build/macos/Build/Products/Release/PS-pocketEdge.app"
[ -d "$APP" ] || { echo "Release app not found: $APP" >&2; exit 1; }
/usr/bin/codesign --verify --deep --strict "$APP"
VERSION=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist")
BUILD_NUMBER=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$APP/Contents/Info.plist")
EXECUTABLE=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APP/Contents/Info.plist")
ARCHS=$(/usr/bin/lipo -archs "$APP/Contents/MacOS/$EXECUTABLE")
case "$ARCHS" in
  *arm64*x86_64*|*x86_64*arm64*) ARCH=universal ;;
  arm64|x86_64) ARCH="$ARCHS" ;;
  *) echo "Unsupported architectures: $ARCHS" >&2; exit 1 ;;
esac
# Keep bundle metadata from becoming path components.
case "$VERSION-$BUILD_NUMBER" in
  *[!a-zA-Z0-9._-]*) echo 'Invalid version metadata.' >&2; exit 1 ;;
esac

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
DMG="$OUT/PS-pocketEdge-$VERSION-$BUILD_NUMBER-macos-$ARCH.dmg"
STAGING=$(mktemp -d "${TMPDIR:-/tmp}/pspocketedge-dmg.XXXXXX")
trap 'rm -rf "$STAGING"' EXIT
/usr/bin/ditto "$APP" "$STAGING/PS-pocketEdge.app"
ln -s /Applications "$STAGING/Applications"
cat > "$STAGING/INSTALL.txt" <<'EOF'
PS-pocketEdge

Drag PS-pocketEdge.app into Applications, then open it from Applications.
Eject this disk image after copying the app.

This installer contains the desktop client only. A running control plane,
PostgreSQL database, and enrolled agent are required. Container management
also requires Docker or Podman on the agent host.

Local builds are not Developer ID signed or notarized for public distribution.
Only open an unnotarized build when you trust its source. On macOS, use
System Settings > Privacy & Security > Open Anyway if macOS offers it.
EOF

/usr/bin/hdiutil create -volname 'PS-pocketEdge' -srcfolder "$STAGING" \
  -format UDZO -ov "$DMG"
/usr/bin/hdiutil verify "$DMG"
(cd "$OUT" && /usr/bin/shasum -a 256 "$(basename "$DMG")" > "$(basename "$DMG").sha256")
printf '\nCreated: %s\nArchitectures: %s\n' "$DMG" "$ARCHS"
printf 'This client-only DMG is not automatically Developer ID signed or notarized.\n'
