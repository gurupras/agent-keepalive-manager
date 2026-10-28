#!/usr/bin/env bash
# build-arch.sh <version> <dist> <out> — one signed pacman generation. Runs in
# an Arch Linux container (publish.sh starts it); uses repo-add from pacman.
#
#   <out>/<arch>/agent-keepalive-<V>-1-<arch>.pkg.tar.zst{,.sig}
#   <out>/<arch>/agent-keepalive.db{,.sig} -> agent-keepalive.db.tar.gz{,.sig}
#   <out>/<arch>/agent-keepalive.files{,.sig}
#
# Published with SigLevel = Required DatabaseRequired: pacman refuses an
# unsigned package or database.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
. "${HERE}/common.sh"
version="${1:?usage: build-arch.sh <version> <dist> <out>}" dist="${2:?}" out="${3:?}"
DB=agent-keepalive
gpg_init

rm -rf "$out"; mkdir -p "$out"
arches=""
for f in "${dist}/agent-keepalive-${version}-1-"*.pkg.tar.zst; do
  [ -f "$f" ] || continue
  a="$(basename "$f" .pkg.tar.zst)"; a="${a##*-}"
  mkdir -p "${out}/${a}"
  install -m 0644 "$f" "${out}/${a}/"
  rgpg --detach-sign --no-armor --output "${out}/${a}/$(basename "$f").sig" "${out}/${a}/$(basename "$f")"
  arches="${arches:+${arches} }${a}"
done
[ -n "$arches" ] || rdie "arch: no agent-keepalive-${version}-1-*.pkg.tar.zst in ${dist}"

for a in $arches; do
  ( cd "${out}/${a}"
    # repo-add signs with gpg itself; the key has no passphrase prompt here
    # because the agent is told to accept a loopback one (common.sh).
    repo-add --quiet --sign --key "$REPO_KEY_FPR" "${DB}.db.tar.gz" ./*.pkg.tar.zst >/dev/null
    # repo-add leaves the short names as symlinks; replace them with copies,
    # so the tree survives any copy that does not keep symlinks.
    for n in "${DB}.db" "${DB}.db.sig" "${DB}.files" "${DB}.files.sig"; do
      [ -L "$n" ] && cp --remove-destination "$(readlink "$n")" "$n"
    done
    [ -s "${DB}.db.sig" ] || rdie "arch: no database signature for ${a}" )
done
printf '%s\n' $arches > "${out}/ARCHES"
chown_back "$out"
rlog "arch: ${version} for ${arches}"
