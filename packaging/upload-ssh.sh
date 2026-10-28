#!/usr/bin/env bash
# upload-ssh.sh — publish release files to the download host (see remote.sh
# for the environment).
#
#   upload-ssh.sh release <version> <dir>   copy every file in <dir> to <root>/<version>/
#   upload-ssh.sh latest <latest.json>      replace <root>/latest.json atomically
#
# `release` never touches latest.json at the root, so a release is invisible to
# updaters until `latest` runs, which the release workflow does last.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=remote.sh
. "${HERE}/remote.sh"
remote_check

case "${1:-}" in
  release)
    version="${2:?usage: upload-ssh.sh release <version> <dir>}"
    dir="${3:?usage: upload-ssh.sh release <version> <dir>}"
    [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || { echo "upload-ssh.sh: bad version ${version}" >&2; exit 64; }
    files=()
    for f in "$dir"/*; do [ -f "$f" ] && files+=("$f"); done
    [ ${#files[@]} -gt 0 ] || { echo "upload-ssh.sh: nothing in ${dir}" >&2; exit 1; }
    remote_put "$version" "${files[@]}"
    echo "[upload] ${#files[@]} files -> ${DOWNLOAD_ROOT}/${version}/" >&2
    ;;
  latest)
    f="${2:?usage: upload-ssh.sh latest <latest.json>}"
    remote_put ".incoming" "$f"
    # rename(2) on the same filesystem: a client reads the old feed or the new one.
    remote_run "mv -f '${DOWNLOAD_ROOT}/.incoming/$(basename "$f")' '${DOWNLOAD_ROOT}/latest.json'"
    echo "[upload] latest.json moved" >&2
    ;;
  *) echo "usage: upload-ssh.sh release|latest ..." >&2; exit 64;;
esac
