#!/usr/bin/env bash
# build-pkg.sh — build agent-keepalive-V-darwin-{x64,arm64}.pkg. Runs on macOS
# (pkgbuild, productbuild, iconutil and sips come with Xcode's command-line tools).
#
# The package installs
#   /usr/local/bin/agent-keepalive               the program (install kind macos-pkg)
#   /Applications/agent-keepalive.app            the desktop window (packaging/lib.sh
#                                                ensure_desktop), which starts the server
#
# Env: PROFILE, VERSION, ARCHES, DIST, and optionally, for a signed and
# notarized package (each step is skipped with a message when unset):
#   MACOS_APP_IDENTITY        "Developer ID Application: …"  codesign the binary and the app
#   MACOS_INSTALLER_IDENTITY  "Developer ID Installer: …"    productbuild --sign
#   MACOS_KEYCHAIN            keychain holding both identities (default: the search list)
#   MACOS_NOTARY_APPLE_ID, MACOS_NOTARY_TEAM_ID, MACOS_NOTARY_PASSWORD
#                             notarytool credentials (app-specific password)
set -euo pipefail
LOG_TAG=macos
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
. "${HERE}/../lib.sh"

[ "$(uname -s)" = Darwin ] || die "the macOS package is built on macOS (pkgbuild/productbuild)"
mkdir -p "$DIST"
IDENT="me.gurupras.agent-keepalive"
# CFBundleVersion and pkgbuild --version want numbers: 1.2.0-rc.1 -> 1.2.0.
core="${VERSION%%-*}"; core="${core%%+*}"

kc=()
[ -n "${MACOS_KEYCHAIN:-}" ] && kc=(--keychain "$MACOS_KEYCHAIN")

sign_code() {
  if [ -z "${MACOS_APP_IDENTITY:-}" ]; then
    log "codesign skipped for $(basename "$1"): no MACOS_APP_IDENTITY"
    return 0
  fi
  codesign --force --timestamp --options runtime ${kc[@]+"${kc[@]}"} --sign "$MACOS_APP_IDENTITY" "$1"
}

# Icon: build the .icns once from the 512 px PNG.
icns="${PKG_ROOT}/bin/stage/macos/agent-keepalive.icns"
if [ ! -s "$icns" ]; then
  iconset="${PKG_ROOT}/bin/stage/macos/agent-keepalive.iconset"
  rm -rf "$iconset"; mkdir -p "$iconset"
  src="${PKG_ROOT}/packaging/icons/agent-keepalive-512.png"
  for s in 16 32 128 256 512; do
    sips -z "$s" "$s" "$src" --out "${iconset}/icon_${s}x${s}.png" >/dev/null
    d=$((s * 2)); [ "$d" -le 512 ] && sips -z "$d" "$d" "$src" --out "${iconset}/icon_${s}x${s}@2x.png" >/dev/null
  done
  iconutil -c icns -o "$icns" "$iconset"
fi

for goarch in $(arches); do
  case "$goarch" in amd64) parch=x86_64; warch=x64;; arm64) parch=arm64; warch=arm64;; esac
  bin="$(ensure_bin macos-pkg darwin "$goarch")"
  stage="${PKG_ROOT}/bin/stage/macos_${goarch}"
  root="${stage}/root"
  rm -rf "$stage"; mkdir -p "${root}/usr/local/bin" "${stage}/scripts"

  install -m 0755 "$bin" "${root}/usr/local/bin/agent-keepalive"
  sign_code "${root}/usr/local/bin/agent-keepalive"

  app="${root}/Applications/agent-keepalive.app"
  mkdir -p "${app}/Contents/MacOS" "${app}/Contents/Resources"
  install -m 0644 "${HERE}/Info.plist" "${app}/Contents/Info.plist"
  subst "${app}/Contents/Info.plist" "VERSION=${VERSION}" "CORE_VERSION=${core}" "IDENT=${IDENT}"
  desktop="$(ensure_desktop darwin "$goarch")"
  [ -n "$desktop" ] || die "the macOS app is the desktop window: it cannot be left out"
  install -m 0755 "$desktop" "${app}/Contents/MacOS/agent-keepalive-desktop"
  sign_code "${app}/Contents/MacOS/agent-keepalive-desktop"
  install -m 0644 "$icns" "${app}/Contents/Resources/agent-keepalive.icns"
  sign_code "$app"

  install -m 0755 "${HERE}/postinstall" "${stage}/scripts/postinstall"

  pkgbuild --root "$root" --identifier "$IDENT" --version "$core" \
    --install-location / --scripts "${stage}/scripts" \
    --ownership recommended "${stage}/component.pkg" >/dev/null

  install -m 0644 "${HERE}/distribution.xml" "${stage}/distribution.xml"
  subst "${stage}/distribution.xml" "VERSION=${VERSION}" "CORE_VERSION=${core}" "IDENT=${IDENT}" "ARCH=${parch}"

  out="${DIST}/agent-keepalive-${VERSION}-darwin-${warch}.pkg"
  sign=()
  if [ -n "${MACOS_INSTALLER_IDENTITY:-}" ]; then
    sign=(--sign "$MACOS_INSTALLER_IDENTITY" ${kc[@]+"${kc[@]}"} --timestamp)
  else
    log "productbuild signing skipped: no MACOS_INSTALLER_IDENTITY"
  fi
  productbuild --distribution "${stage}/distribution.xml" --package-path "$stage" \
    ${sign[@]+"${sign[@]}"} "$out" >/dev/null

  if [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] && [ ${#sign[@]} -gt 0 ]; then
    xcrun notarytool submit "$out" --wait \
      --apple-id "$MACOS_NOTARY_APPLE_ID" --team-id "$MACOS_NOTARY_TEAM_ID" \
      --password "$MACOS_NOTARY_PASSWORD"
    xcrun stapler staple "$out"
  else
    log "notarization skipped: needs MACOS_NOTARY_APPLE_ID and a signed package"
  fi
  log "$(basename "$out")"
done
