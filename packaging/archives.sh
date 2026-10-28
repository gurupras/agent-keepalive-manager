#!/usr/bin/env bash
# archives.sh — the plain archives:
#
#   agent-keepalive_V_{linux,darwin}_{amd64,arm64}.tar.gz    install kind "archive"
#   agent-keepalive_V_windows_{amd64,arm64}.zip
#   agent-keepalive-hub_V_linux_{amd64,arm64}.tar.gz         (with --hub)
#
#   packaging/archives.sh [--hub] [goos...]     (default: linux darwin windows)
#
# Each holds one top-level directory named like the archive, with the binary,
# LICENSE and README.md. The Homebrew formula installs from these.
set -euo pipefail
LOG_TAG=archive
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "${HERE}/lib.sh"

hub=0
if [ "${1:-}" = "--hub" ]; then hub=1; shift; fi
oses=("$@")
if [ ${#oses[@]} -eq 0 ]; then
  if [ "$hub" -eq 1 ]; then oses=(linux); else oses=(linux darwin windows); fi
fi
mkdir -p "$DIST"

zipdir() { # zipdir <parent> <dir> <out.zip>
  if command -v zip >/dev/null 2>&1; then
    ( cd "$1" && rm -f "$3" && zip -qr -X "$3" "$2" )
  else
    python3 - "$1" "$2" "$3" <<'PY'
import os, sys, zipfile
parent, d, out = sys.argv[1:]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(os.path.join(parent, d)):
        for f in sorted(files):
            p = os.path.join(root, f)
            z.write(p, os.path.relpath(p, parent))
PY
  fi
}

for goos in "${oses[@]}"; do
  for goarch in $(arches); do
    if [ "$hub" -eq 1 ]; then
      name="agent-keepalive-hub_${VERSION}_${goos}_${goarch}"
      bin="$(ensure_hub_bin "$goos" "$goarch")"
    else
      name="agent-keepalive_${VERSION}_${goos}_${goarch}"
      bin="$(ensure_bin archive "$goos" "$goarch")"
    fi
    stage="${PKG_ROOT}/bin/stage/archive"
    rm -rf "${stage:?}/${name}"; mkdir -p "${stage}/${name}"
    install -m 0755 "$bin" "${stage}/${name}/"
    install -m 0644 "${PKG_ROOT}/LICENSE" "${stage}/${name}/"
    [ -f "${PKG_ROOT}/README.md" ] && install -m 0644 "${PKG_ROOT}/README.md" "${stage}/${name}/"
    if [ "$goos" = windows ]; then
      zipdir "$stage" "$name" "${DIST}/${name}.zip"
      log "${name}.zip"
    else
      # Reproducible: fixed order, owner and mtime.
      tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
        -C "$stage" -czf "${DIST}/${name}.tar.gz" "$name" 2>/dev/null \
        || tar -C "$stage" -czf "${DIST}/${name}.tar.gz" "$name"
      log "${name}.tar.gz"
    fi
  done
done
