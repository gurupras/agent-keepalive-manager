#!/usr/bin/env bash
# build-apt.sh <version> <dist> <out> — one signed APT generation. Runs in a
# Debian container (publish.sh starts it); needs apt-utils and gnupg.
#
#   <out>/pool/main/a/agent-keepalive/agent-keepalive_<V>_<arch>.deb
#   <out>/dists/stable/main/binary-<arch>/Packages{,.gz}
#   <out>/dists/stable/Release       hashes of every index
#   <out>/dists/stable/InRelease     the same, inline-signed (what apt reads)
#   <out>/dists/stable/Release.gpg   detached signature (older clients)
#
# InRelease is signed by the repository key and carries the hash of every
# Packages file, which carries the hash of every .deb.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
. "${HERE}/common.sh"
version="${1:?usage: build-apt.sh <version> <dist> <out>}" dist="${2:?}" out="${3:?}"
SUITE=stable COMPONENT=main
gpg_init

rm -rf "$out"; mkdir -p "${out}/pool/${COMPONENT}/a/agent-keepalive"
archs=""
for f in "${dist}/agent-keepalive_${version}_"*.deb; do
  [ -f "$f" ] || continue
  a="$(dpkg-deb --field "$f" Architecture)"
  install -m 0644 "$f" "${out}/pool/${COMPONENT}/a/agent-keepalive/"
  archs="${archs:+${archs} }${a}"
done
[ -n "$archs" ] || rdie "apt: no agent-keepalive_${version}_*.deb in ${dist}"

cd "$out"
for a in $archs; do
  d="dists/${SUITE}/${COMPONENT}/binary-${a}"
  mkdir -p "$d"
  apt-ftparchive --arch "$a" packages pool > "${d}/Packages"
  [ -s "${d}/Packages" ] || rdie "apt: empty Packages for ${a}"
  gzip -9nc "${d}/Packages" > "${d}/Packages.gz"
done
apt-ftparchive \
  -o "APT::FTPArchive::Release::Origin=agent-keepalive" \
  -o "APT::FTPArchive::Release::Label=agent-keepalive" \
  -o "APT::FTPArchive::Release::Suite=${SUITE}" \
  -o "APT::FTPArchive::Release::Codename=${SUITE}" \
  -o "APT::FTPArchive::Release::Architectures=${archs}" \
  -o "APT::FTPArchive::Release::Components=${COMPONENT}" \
  -o "APT::FTPArchive::Release::Description=agent-keepalive ${version}" \
  release "dists/${SUITE}" > Release.tmp
mv Release.tmp "dists/${SUITE}/Release"
rgpg --clearsign --output "dists/${SUITE}/InRelease" "dists/${SUITE}/Release"
rgpg --armor --detach-sign --output "dists/${SUITE}/Release.gpg" "dists/${SUITE}/Release"
chown_back "$out"
rlog "apt: ${version} for ${archs}"
