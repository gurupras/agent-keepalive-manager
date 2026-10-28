#!/usr/bin/env bash
# version.sh — print the version this tree builds, without a leading "v".
#
#   tag v1.4.0 on HEAD   -> 1.4.0
#   anything else        -> 0.0.0-dev.N.gSHA   (N = commits since the last v*
#                           tag, or since the first commit when there is none)
#
# VERSION in the environment wins, so CI can pin what it builds.
set -euo pipefail

if [ -n "${VERSION:-}" ]; then
  printf '%s\n' "${VERSION#v}"
  exit 0
fi

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if ! git rev-parse --git-dir >/dev/null 2>&1 || ! git rev-parse HEAD >/dev/null 2>&1; then
  echo "0.0.0-dev.0.gunknown"
  exit 0
fi

if tag="$(git describe --tags --exact-match --match 'v[0-9]*' HEAD 2>/dev/null)"; then
  printf '%s\n' "${tag#v}"
  exit 0
fi

sha="$(git rev-parse --short=7 HEAD)"
if last="$(git describe --tags --abbrev=0 --match 'v[0-9]*' HEAD 2>/dev/null)"; then
  n="$(git rev-list --count "${last}..HEAD")"
else
  n="$(git rev-list --count HEAD)"
fi
printf '0.0.0-dev.%s.g%s\n' "$n" "$sha"
