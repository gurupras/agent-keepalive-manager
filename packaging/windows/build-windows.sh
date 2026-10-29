#!/usr/bin/env bash
# build-windows.sh — build the Windows setup and portable executables:
#
#   agent-keepalive-V-setup-{x64,arm64}.exe      NSIS installer (windows-setup)
#   agent-keepalive-V-portable-{x64,arm64}.exe   the binary with the desktop window
#                                                built in (windows-portable)
#
# The desktop window (packaging/lib.sh ensure_desktop) is built on Windows;
# put it in bin/desktop/windows_<arch>/ first, or set DESKTOP=none.
#
#   packaging/windows/build-windows.sh [setup] [portable]   (default: both)
#
# Env: PROFILE, VERSION, ARCHES, DIST, MAKENSIS (default: makensis on PATH,
# else run in a debian container), and the signing variables of sign.sh.
set -euo pipefail
LOG_TAG=windows
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
. "${HERE}/../lib.sh"

what=("$@")
[ ${#what[@]} -gt 0 ] || what=(setup portable)
mkdir -p "$DIST"

# VIProductVersion must be four numbers: 1.2.0-rc.1 -> 1.2.0.0.
core="${VERSION%%-*}"; core="${core%%+*}"
IFS=. read -r v1 v2 v3 _ <<< "$core"
fileversion="${v1:-0}.${v2:-0}.${v3:-0}.0"

signing=0
[ -n "${WINDOWS_SIGN_PFX:-}${WINDOWS_SIGN_PFX_BASE64:-}" ] && signing=1

makensis_run() {
  local m="${MAKENSIS:-$(command -v makensis || true)}"
  if [ -n "$m" ]; then
    "$m" "$@"
    return
  fi
  command -v docker >/dev/null 2>&1 || die "makensis not found (apt install nsis) and no docker to run it in"
  log "makensis not on PATH: running it in a debian container"
  local owner; owner="$(id -u):$(id -g)"
  docker run --rm -v "${PKG_ROOT}:${PKG_ROOT}" -v "${DIST}:${DIST}" -w "$PWD" -e OWNER="$owner" -e DEBIAN_FRONTEND=noninteractive \
    debian:stable-slim sh -c '
      apt-get update -qq >/dev/null && apt-get install -y -qq nsis >/dev/null || exit 1
      makensis "$@"; rc=$?
      for a in "$@"; do case "$a" in -DOUTFILE=*) chown "$OWNER" "${a#-DOUTFILE=}" 2>/dev/null;; esac; done
      exit $rc' makensis "$@"
}

for goarch in $(arches); do
  warch="$(win_arch "$goarch")"
  for w in "${what[@]}"; do
    case "$w" in
      portable)
        desktop="$(ensure_desktop windows "$goarch")"
        if [ -n "$desktop" ]; then
          # Signed before it is built in: the portable exe writes it out and runs it.
          embed="${PKG_ROOT}/bin/stage/windows-portable_${goarch}/agent-keepalive-desktop.exe"
          mkdir -p "$(dirname "$embed")"
          install -m 0755 "$desktop" "$embed"
          "${HERE}/sign.sh" "$embed"
          bin="${PKG_ROOT}/bin/windows-portable/windows_${goarch}/agent-keepalive.exe"
          DESKTOP_EMBED="$embed" "${PKG_ROOT}/scripts/build-go.sh" windows "$goarch" windows-portable "$bin" >&2
        else
          bin="$(ensure_bin windows-portable windows "$goarch")"
        fi
        out="${DIST}/agent-keepalive-${VERSION}-portable-${warch}.exe"
        install -m 0755 "$bin" "$out"
        "${HERE}/sign.sh" "$out"
        log "$(basename "$out")"
        ;;
      setup)
        bin="$(ensure_bin windows-setup windows "$goarch")"
        stage="${PKG_ROOT}/bin/stage/windows-setup_${goarch}"
        rm -rf "$stage"; mkdir -p "$stage"
        install -m 0755 "$bin" "${stage}/agent-keepalive.exe"
        "${HERE}/sign.sh" "${stage}/agent-keepalive.exe"
        desktop="$(ensure_desktop windows "$goarch")"
        out="${DIST}/agent-keepalive-${VERSION}-setup-${warch}.exe"
        args=(-V2 -NOCD
          "-DVERSION=${VERSION}" "-DFILEVERSION=${fileversion}" "-DARCH=${warch}"
          "-DBINARY=${stage}/agent-keepalive.exe" "-DOUTFILE=${out}"
          "-DICON=${PKG_ROOT}/packaging/icons/agent-keepalive.ico"
          "-DLICENSEFILE=${PKG_ROOT}/LICENSE")
        if [ -n "$desktop" ]; then
          install -m 0755 "$desktop" "${stage}/agent-keepalive-desktop.exe"
          "${HERE}/sign.sh" "${stage}/agent-keepalive-desktop.exe"
          args+=("-DDESKTOP=${stage}/agent-keepalive-desktop.exe")
        fi
        [ "$signing" -eq 1 ] && args+=("-DSIGNCMD=${HERE}/sign.sh")
        ( cd "$HERE" && makensis_run "${args[@]}" "${HERE}/installer.nsi" )
        [ -s "$out" ] || die "makensis produced no $(basename "$out")"
        [ "$signing" -eq 1 ] || "${HERE}/sign.sh" "$out"
        log "$(basename "$out")"
        ;;
      *) die "unknown target ${w} (setup, portable)";;
    esac
  done
done
