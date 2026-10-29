#!/usr/bin/env bash
# build-appimage.sh — build agent-keepalive-V-{x86_64,aarch64}.AppImage.
#
# Env: PROFILE, VERSION, ARCHES ("amd64 arm64"), DIST,
#      APPIMAGETOOL (default: appimagetool on PATH, else downloaded to bin/tools),
#      APPIMAGE_RUNTIME_DIR (holds runtime-x86_64 / runtime-aarch64; default:
#      downloaded to bin/tools).
#
# The binary is static, so the AppDir is just AppRun, the binary, the desktop
# window, a .desktop file and the icon. The window uses the system's
# WebKitGTK 4.1 rather than carrying its own; where there is none, the
# AppImage opens the UI in a browser instead. appimagetool runs on this (x86_64) machine for both
# architectures: only the runtime prepended to the image is per-architecture,
# and it is passed in with --runtime-file.
set -euo pipefail
LOG_TAG=appimage
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
. "${HERE}/../lib.sh"

TOOLS="${PKG_ROOT}/bin/tools"
RELEASES="https://github.com/AppImage"
mkdir -p "$TOOLS" "$DIST"

fetch() {
  local url="$1" out="$2"
  [ -s "$out" ] && return 0
  log "downloading ${url}"
  curl -fsSL --retry 3 -o "${out}.part" "$url"
  mv "${out}.part" "$out"
  chmod +x "$out"
}

tool="${APPIMAGETOOL:-$(command -v appimagetool || true)}"
if [ -z "$tool" ]; then
  [ "$(uname -m)" = x86_64 ] || die "no appimagetool on PATH and no download for $(uname -m)"
  tool="${TOOLS}/appimagetool-x86_64.AppImage"
  fetch "${RELEASES}/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage" "$tool"
fi

for goarch in $(arches); do
  arch="$(uname_arch "$goarch")"
  runtime="${APPIMAGE_RUNTIME_DIR:-$TOOLS}/runtime-${arch}"
  fetch "${RELEASES}/type2-runtime/releases/download/continuous/runtime-${arch}" "$runtime"

  bin="$(ensure_bin appimage linux "$goarch")"
  appdir="${PKG_ROOT}/bin/stage/appimage_${goarch}/agent-keepalive.AppDir"
  rm -rf "$appdir"; mkdir -p "${appdir}/usr/bin" "${appdir}/usr/share/icons/hicolor/256x256/apps"
  install -m 0755 "$bin" "${appdir}/usr/bin/agent-keepalive"
  desktop="$(ensure_desktop linux "$goarch")"
  [ -z "$desktop" ] || install -m 0755 "$desktop" "${appdir}/usr/bin/agent-keepalive-desktop"
  install -m 0755 "${HERE}/AppRun" "${appdir}/AppRun"
  install -m 0644 "${HERE}/agent-keepalive.desktop" "${appdir}/agent-keepalive.desktop"
  install -m 0644 "${PKG_ROOT}/packaging/icons/agent-keepalive.png" "${appdir}/agent-keepalive.png"
  install -m 0644 "${PKG_ROOT}/packaging/icons/agent-keepalive.png" "${appdir}/usr/share/icons/hicolor/256x256/apps/"
  ln -s agent-keepalive.png "${appdir}/.DirIcon"

  out="${DIST}/agent-keepalive-${VERSION}-${arch}.AppImage"
  rm -f "$out"
  # --appimage-extract-and-run: CI containers have no FUSE to mount the tool.
  ARCH="$arch" VERSION="$VERSION" "$tool" --appimage-extract-and-run --no-appstream \
    --runtime-file "$runtime" "$appdir" "$out" >&2
  chmod 0755 "$out"
  log "$(basename "$out")"
done
