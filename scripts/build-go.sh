#!/usr/bin/env bash
# build-go.sh — build one binary for one target and one install kind.
#
#   scripts/build-go.sh [--hub] <goos> <goarch> <kind> [out]
#
# Default output: bin/<kind>/<goos>_<goarch>/agent-keepalive[.exe]
# (bin/hub/<goos>_<goarch>/agent-keepalive-hub with --hub). Packaging scripts
# look for their binaries there and call this when one is missing.
#
# The profile comes from $PROFILE (see scripts/profile.sh), the version from
# scripts/version.sh (or $VERSION). Always CGO_ENABLED=0 and -trimpath, so the
# result is one static file that is the same on every build machine.
#
# DESKTOP_EMBED=<file> builds the desktop window into the binary (the Windows
# portable exe, which must stay one file); `agent-keepalive app` writes it out
# to the data directory.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
hub=0
if [ "${1:-}" = "--hub" ]; then hub=1; shift; fi
[ $# -ge 3 ] || { echo "usage: build-go.sh [--hub] <goos> <goarch> <kind> [out]" >&2; exit 64; }
goos="$1" goarch="$2" kind="$3"

ext=""; [ "$goos" = windows ] && ext=".exe"
if [ "$hub" -eq 1 ]; then
  pkg=./cmd/agent-keepalive-hub
  out="${4:-${ROOT}/bin/hub/${goos}_${goarch}/agent-keepalive-hub${ext}}"
  # The hub has no install kind; "archive" keeps profile.sh's check happy and
  # the linker ignores variables the hub does not have.
  kind=archive
else
  pkg=./cmd/agent-keepalive
  out="${4:-${ROOT}/bin/${kind}/${goos}_${goarch}/agent-keepalive${ext}}"
fi

version="$("${ROOT}/scripts/version.sh")"
ldflags="$("${ROOT}/scripts/profile.sh" ldflags --kind "$kind" --version "$version")"

tags=()
if [ -n "${DESKTOP_EMBED:-}" ]; then
  [ -s "$DESKTOP_EMBED" ] || { echo "build-go: DESKTOP_EMBED=${DESKTOP_EMBED} is not a file" >&2; exit 1; }
  embed="${ROOT}/cmd/agent-keepalive/desktop/agent-keepalive-desktop.bin"
  mkdir -p "$(dirname "$embed")"
  cp "$DESKTOP_EMBED" "$embed"
  trap 'rm -f "$embed"' EXIT
  tags=(-tags desktopembed)
fi

mkdir -p "$(dirname "$out")"
echo "build-go: ${pkg} ${goos}/${goarch} kind=${kind} version=${version} profile=${PROFILE:-none}${DESKTOP_EMBED:+ +desktop} -> ${out#"${ROOT}"/}" >&2
cd "$ROOT"
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
  go build -trimpath ${tags[@]+"${tags[@]}"} -ldflags "$ldflags" -o "$out" "$pkg"
