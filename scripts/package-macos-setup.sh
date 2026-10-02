#!/bin/bash
# Build a universal native Setup app with the dashboard and Go services inside.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_CLIENT=true
case "${1:-}" in
  --skip-client-build) BUILD_CLIENT=false ;;
  --help) echo 'Usage: bash scripts/package-macos-setup.sh [--skip-client-build]'; exit 0 ;;
  '') ;;
  *) echo 'Unknown option' >&2; exit 1 ;;
esac
[ "$(uname -s)" = Darwin ] || { echo 'This build requires macOS and Xcode.' >&2; exit 1; }
if [ "$BUILD_CLIENT" = true ]; then
  (cd "$ROOT/app" && flutter clean && flutter pub get && flutter build macos --release)
fi
APP="$ROOT/app/build/macos/Build/Products/Release/PS-pocketEdge.app"
/usr/bin/codesign --verify --deep --strict "$APP"
VERSION=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist")
BUILD_NUMBER=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$APP/Contents/Info.plist")
case "$VERSION-$BUILD_NUMBER" in *[!a-zA-Z0-9._-]*) echo 'Invalid version' >&2; exit 1 ;; esac
mkdir -p "$ROOT/dist"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/pspe-setup-build.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
STAGING="$WORK/image"
SETUP="$STAGING/PS-pocketEdge Setup.app"
mkdir -p "$SETUP/Contents/MacOS" "$SETUP/Contents/Resources/payload"
PAYLOAD="$SETUP/Contents/Resources/payload"
/usr/bin/ditto "$APP" "$PAYLOAD/PS-pocketEdge.app"
printf '%s\n' "$VERSION" > "$PAYLOAD/version.txt"
for arch in arm64 amd64; do
  mkdir -p "$WORK/$arch"
  echo "Building bundled services for ${arch}..."
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" GOMAXPROCS=2 \
    go build -p 2 -ldflags "-X github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version.Version=$VERSION" \
    -o "$WORK/$arch/" ./cmd/agent ./cmd/controlplane ./cmd/setup)
done
for binary in agent controlplane setup; do
  case "$binary" in
    setup) target="$SETUP/Contents/MacOS/pe-setup-helper" ;;
    *) target="$PAYLOAD/pe-$binary" ;;
  esac
  /usr/bin/lipo -create "$WORK/arm64/$binary" "$WORK/amd64/$binary" -output "$target"
  chmod 755 "$target"
  /usr/bin/codesign --force --sign - "$target"
done
for arch in arm64 x86_64; do
  echo "Building native setup wizard for ${arch}..."
  xcrun swiftc -parse-as-library -target "$arch-apple-macosx14.0" \
    "$ROOT/installer/macos/Setup.swift" -o "$WORK/Setup-$arch"
done
/usr/bin/lipo -create "$WORK/Setup-arm64" "$WORK/Setup-x86_64" -output "$SETUP/Contents/MacOS/Setup"
chmod 755 "$SETUP/Contents/MacOS/Setup"
cat > "$SETUP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>Setup</string>
<key>CFBundleIdentifier</key><string>com.pspocketedge.setup</string>
<key>CFBundleName</key><string>PS-pocketEdge Setup</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$VERSION</string>
<key>CFBundleVersion</key><string>$BUILD_NUMBER</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSAppleEventsUsageDescription</key><string>Setup opens Terminal when you choose to install Homebrew so you can complete its administrator prompts.</string>
</dict></plist>
EOF
/usr/bin/codesign --force --sign - "$SETUP"
/usr/bin/codesign --verify --deep --strict "$SETUP"
# Verify both architectures in each shipped executable, including nested code.
for binary in "$SETUP/Contents/MacOS/Setup" "$SETUP/Contents/MacOS/pe-setup-helper" "$PAYLOAD/pe-agent" "$PAYLOAD/pe-controlplane"; do
  /usr/bin/lipo "$binary" -verify_arch arm64 x86_64
done
cat > "$STAGING/INSTALL.txt" <<'EOF'
Open PS-pocketEdge Setup.app to begin guided setup.
Quick install uses reviewed defaults; Custom install exposes dependency,
connection, location, VM resource, and startup choices.

Local setup installs Docker Engine through Colima (or Podman), PostgreSQL,
the control plane, an enrolled agent, and the desktop app. It never installs
Docker Desktop. Downloads require internet access. Existing-server mode
installs the desktop app only.

Setup requires macOS 14 or newer. At least 8 GB free space is checked for
local setup; container images and database growth may need additional space.
Quit PS-pocketEdge before installation. Keep this DMG until setup finishes.

This local test build is ad hoc signed, not Developer ID signed or notarized.
Public distribution requires Apple signing and notarization.
EOF
DMG="$ROOT/dist/PS-pocketEdge-Setup-$VERSION-$BUILD_NUMBER-macos-universal.dmg"
/usr/bin/hdiutil create -volname 'PS-pocketEdge Setup' -srcfolder "$STAGING" -format UDZO -ov "$DMG"
/usr/bin/hdiutil verify "$DMG"
(cd "$ROOT/dist" && shasum -a 256 "$(basename "$DMG")" > "$(basename "$DMG").sha256")
echo "Created: $DMG"
