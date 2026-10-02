# macOS installer

For Quick/Custom setup that installs supporting services, use the
[guided macOS setup installer](guided-macos-setup.md). This page describes the
separate client-only DMG.

The DMG installs the **PS-pocketEdge desktop client** by dragging it into
Applications. It includes the resource bar and host terminal UI. It does not
install or start the control plane, PostgreSQL, an agent, Docker, or Podman.
Keep the current development services running to use it on this Mac.

## Build

Run on macOS with Flutter and Xcode installed:

```sh
bash scripts/package-macos.sh
```

The script builds the Release app, verifies its code signature, creates a
compressed DMG in `dist/`, verifies the image, and writes a SHA-256 checksum.
The filename includes the app version, build number, and actual CPU
architectures. Update `version:` in `app/pubspec.yaml` before a new release.
`universal` means the executable contains both Intel and Apple Silicon code.

The default API address is `http://localhost:8080`. For a control plane hosted
elsewhere, build a separate installer with its address:

```sh
bash scripts/package-macos.sh \
  --control-plane-url https://your-control-plane.example.com \
  --output-dir dist/remote
```

The API address is compiled into the app; changing it requires rebuilding.
`--skip-build` packages the existing Release app and preserves its compiled
configuration. It is useful after building or signing the app separately.

## Install and check

1. Open the DMG and drag **PS-pocketEdge.app** onto **Applications**.
2. Quit any previous development instance, then open the installed app from
   Applications. Eject the DMG after copying.
3. With the configured control plane running, confirm that your servers load,
   the thin bottom bar displays resources, and **Terminal** opens on a connected
   agent as an administrator.

For a new Mac, first provision a control plane and PostgreSQL, configure the
client's API address, and enroll an agent. See [local Docker and Podman setup](local-docker.md)
for runtime and agent requirements. This DMG is not yet an all-in-one installer.

## Public distribution

The default Xcode configuration uses ad hoc signing. That is suitable for local
testing, but is not a Developer ID signature or notarization. macOS may block
the downloaded app. For a build you trust, use **System Settings → Privacy &
Security → Open Anyway** when offered; do not disable Gatekeeper globally.

Before sharing a public installer:

1. Use an Apple Developer account and **Developer ID Application** certificate.
   Configure distribution signing, hardened runtime, and appropriate release
   entitlements in Xcode. Sign the nested frameworks and app using Apple's
   distribution workflow, then verify the resulting app.
2. Package that signed Release app with `--skip-build`. Sign the DMG with your
   Developer ID Application identity.
3. Submit the DMG with `xcrun notarytool submit ... --keychain-profile ... --wait`.
   When Apple accepts it, staple the ticket with `xcrun stapler staple ...` and
   validate with `xcrun stapler validate ...`.
4. Regenerate the SHA-256 checksum after signing/stapling, and test a downloaded
   copy on a clean Mac with the supported architecture and macOS version.

Signing and notarization require your certificate and Apple credentials; the
packaging script does not upload artifacts or access those credentials.
See Apple's [packaging guidance](https://developer.apple.com/documentation/xcode/packaging-mac-software-for-distribution)
and [notarization workflow](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).
