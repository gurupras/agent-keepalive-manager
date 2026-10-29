#!/usr/bin/env bash
# build-desktop.sh — build the desktop window (desktop/, Tauri) for one target.
#
#   scripts/build-desktop.sh <goos> <goarch> [out]
#
# Default output: bin/desktop/<goos>_<goarch>/agent-keepalive-desktop[.exe],
# where the packaging scripts look for it (packaging/lib.sh ensure_desktop).
#
# The window links the platform's webview (WebKitGTK, WebView2, WKWebView),
# so each OS builds its own: Linux on Linux with libwebkit2gtk-4.1-dev,
# Windows on Windows, macOS on macOS. Windows and macOS build their other
# architecture too once its Rust target is installed (rustup target add);
# Linux needs the other architecture's libraries, so it builds natively only.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ $# -ge 2 ] || { echo "usage: build-desktop.sh <goos> <goarch> [out]" >&2; exit 64; }
goos="$1" goarch="$2"
case "${goos}/${goarch}" in
  linux/amd64)   triple=x86_64-unknown-linux-gnu;;
  linux/arm64)   triple=aarch64-unknown-linux-gnu;;
  windows/amd64) triple=x86_64-pc-windows-msvc;;
  windows/arm64) triple=aarch64-pc-windows-msvc;;
  darwin/amd64)  triple=x86_64-apple-darwin;;
  darwin/arm64)  triple=aarch64-apple-darwin;;
  *) echo "build-desktop: no desktop window for ${goos}/${goarch}" >&2; exit 64;;
esac
ext=""; [ "$goos" = windows ] && ext=".exe"
out="${3:-${ROOT}/bin/desktop/${goos}_${goarch}/agent-keepalive-desktop${ext}}"

version="$("${ROOT}/scripts/version.sh")"
echo "build-desktop: ${goos}/${goarch} (${triple}) version=${version} -> ${out#"${ROOT}"/}" >&2
mkdir -p "$(dirname "$out")"
cd "${ROOT}/desktop"
AGENT_KEEPALIVE_VERSION="$version" cargo build --release --locked --target "$triple"
cp "target/${triple}/release/agent-keepalive-desktop${ext}" "$out"
chmod 0755 "$out"
