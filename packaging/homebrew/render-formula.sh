#!/usr/bin/env bash
# render-formula.sh — write the Homebrew formula for a release.
#
#   packaging/homebrew/render-formula.sh <version> <base-url> <SHA256SUMS> <out.rb>
#
# <base-url> is where the archives are downloadable, e.g.
# https://github.com/gurupras/agent-keepalive-manager/releases/download/v1.2.0
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[ $# -eq 4 ] || { echo "usage: render-formula.sh <version> <base-url> <SHA256SUMS> <out.rb>" >&2; exit 64; }
version="$1" base="${2%/}" sums="$3" out="$4"
homepage="${HOMEPAGE:-https://github.com/gurupras/agent-keepalive-manager}"

sha() {
  local name="agent-keepalive_${version}_$1.tar.gz" s
  s="$(awk -v n="$name" '$2==n || $2=="*"n {print $1}' "$sums")"
  [ -n "$s" ] || { echo "render-formula.sh: ${name} is not in ${sums}" >&2; exit 1; }
  printf '%s' "$s"
}

sed -e "s|@VERSION@|${version}|g" -e "s|@BASE_URL@|${base}|g" -e "s|@HOMEPAGE@|${homepage}|g" \
    -e "s|@SHA_DARWIN_ARM64@|$(sha darwin_arm64)|" -e "s|@SHA_DARWIN_AMD64@|$(sha darwin_amd64)|" \
    -e "s|@SHA_LINUX_ARM64@|$(sha linux_arm64)|"   -e "s|@SHA_LINUX_AMD64@|$(sha linux_amd64)|" \
    "${HERE}/agent-keepalive.rb.in" > "$out"
if grep -q '@[A-Z_0-9]*@' "$out"; then
  echo "render-formula.sh: placeholders left in ${out}" >&2; exit 1
fi
echo "[brew] ${out}" >&2
