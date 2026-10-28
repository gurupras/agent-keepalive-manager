#!/usr/bin/env bash
# lib.sh — shared by the packaging scripts. Sourced, not run.
#
# Every script writes its release artifacts to $DIST (default dist/) under the
# names in docs/release-artifacts.md, and its intermediate files under bin/.

PKG_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${DIST:-${PKG_ROOT}/dist}"
VERSION="$("${PKG_ROOT}/scripts/version.sh")"
export VERSION

log()  { printf '[%s] %s\n' "${LOG_TAG:-package}" "$*" >&2; }
die()  { printf '[%s] ERROR %s\n' "${LOG_TAG:-package}" "$*" >&2; exit 1; }

profile_get() { "${PKG_ROOT}/scripts/profile.sh" get "$1"; }

# ensure_bin <kind> <goos> <goarch>: print the path of the binary for that
# kind and target, building it if it is not there yet. REBUILD=1 always builds.
ensure_bin() {
  local kind="$1" goos="$2" goarch="$3" ext=""
  [ "$goos" = windows ] && ext=".exe"
  local out="${PKG_ROOT}/bin/${kind}/${goos}_${goarch}/agent-keepalive${ext}"
  if [ ! -x "$out" ] || [ "${REBUILD:-0}" = 1 ]; then
    "${PKG_ROOT}/scripts/build-go.sh" "$goos" "$goarch" "$kind" "$out" >&2
  fi
  printf '%s\n' "$out"
}

ensure_hub_bin() {
  local goos="$1" goarch="$2" ext=""
  [ "$goos" = windows ] && ext=".exe"
  local out="${PKG_ROOT}/bin/hub/${goos}_${goarch}/agent-keepalive-hub${ext}"
  if [ ! -x "$out" ] || [ "${REBUILD:-0}" = 1 ]; then
    "${PKG_ROOT}/scripts/build-go.sh" --hub "$goos" "$goarch" archive "$out" >&2
  fi
  printf '%s\n' "$out"
}

# Architecture spellings: Go (amd64/arm64) -> the kernel's (x86_64/aarch64)
# and Windows' (x64/arm64).
uname_arch() { case "$1" in amd64) echo x86_64;; arm64) echo aarch64;; *) die "unknown arch $1";; esac; }
win_arch()   { case "$1" in amd64) echo x64;;    arm64) echo arm64;;   *) die "unknown arch $1";; esac; }

# ARCHES: which Go architectures to package (default both).
arches() { printf '%s\n' ${ARCHES:-amd64 arm64}; }

# subst <file> KEY=value...: replace @KEY@ in a file, in place.
subst() {
  local f="$1" kv k v; shift
  for kv in "$@"; do
    k="${kv%%=*}" v="${kv#*=}"
    v="${v//\\/\\\\}"; v="${v//|/\\|}"; v="${v//&/\\&}"
    # Not sed -i: its syntax differs between GNU and BSD (macOS) sed.
    sed "s|@${k}@|${v}|g" "$f" > "${f}.subst" && mv "${f}.subst" "$f"
  done
}
