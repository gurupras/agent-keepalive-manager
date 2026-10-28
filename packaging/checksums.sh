#!/usr/bin/env bash
# checksums.sh — write dist/SHA256SUMS over every release artifact in dist/
# (everything except SHA256SUMS itself and latest.json, which carries its own
# hashes and is signed).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST="${DIST:-${HERE}/../dist}"
cd "$DIST"
files=()
for f in *; do
  [ -f "$f" ] || continue
  case "$f" in SHA256SUMS|latest.json|*.sig|*.asc) continue;; esac
  files+=("$f")
done
[ ${#files[@]} -gt 0 ] || { echo "checksums.sh: no artifacts in ${DIST}" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${files[@]}" > SHA256SUMS
else
  shasum -a 256 "${files[@]}" > SHA256SUMS
fi
echo "[checksums] SHA256SUMS: ${#files[@]} files" >&2
