#!/usr/bin/env bash
# build-rpm.sh — signed RPM packages and repository. Runs in a Fedora
# container (publish.sh starts it); needs rpm-sign, createrepo_c and gnupg2.
#
#   build-rpm.sh sign <dir>                 sign every .rpm in <dir> in place
#   build-rpm.sh repo <version> <dist> <out>
#
#   <out>/<basearch>/agent-keepalive-<V>-1.<basearch>.rpm   signed package
#   <out>/<basearch>/repodata/repomd.xml{,.asc}              signed metadata
#
# dnf checks both: repo_gpgcheck=1 the metadata signature, gpgcheck=1 each
# package's own signature (which also covers an rpm downloaded on its own).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
. "${HERE}/common.sh"
gpg_init

sign_rpm() {
  local extra="--batch --pinentry-mode loopback"
  [ -n "${REPO_SIGNING_PASSPHRASE:-}" ] && extra="${extra} --passphrase ${REPO_SIGNING_PASSPHRASE}"
  rpmsign --define "_gpg_name ${REPO_KEY_FPR}" --define "_gpg_sign_cmd_extra_args ${extra}" \
    --addsign "$1" >/dev/null
  # Old rpm prints "Signature : RSA/SHA256, …, Key ID …"; rpm 6 an
  # OpenPGP "…signature, key ID …: NOKEY" warning. Unsigned prints neither.
  rpm -qpi "$1" 2>&1 | grep -qi 'key id' \
    || rdie "rpm: $(basename "$1") is unsigned after rpmsign"
}

case "${1:-}" in
  sign)
    dir="${2:?usage: build-rpm.sh sign <dir>}"
    n=0
    for f in "$dir"/*.rpm; do [ -f "$f" ] || continue; sign_rpm "$f"; n=$((n + 1)); done
    chown_back "$dir"
    rlog "rpm: signed ${n} packages"
    ;;
  repo)
    version="${2:?}" dist="${3:?}" out="${4:?}"
    rm -rf "$out"; mkdir -p "$out"
    arches=""
    for f in "${dist}/agent-keepalive-${version}-1."*.rpm; do
      [ -f "$f" ] || continue
      a="$(rpm -qp --qf '%{ARCH}' "$f" 2>/dev/null)"
      mkdir -p "${out}/${a}"
      install -m 0644 "$f" "${out}/${a}/"
      sign_rpm "${out}/${a}/$(basename "$f")"
      arches="${arches:+${arches} }${a}"
    done
    [ -n "$arches" ] || rdie "rpm: no agent-keepalive-${version}-1.*.rpm in ${dist}"
    for a in $arches; do
      # gzip metadata: older dnf/yum cannot read createrepo_c's zstd default.
      createrepo_c --quiet --no-database --general-compress-type=gz "${out}/${a}"
      rgpg --armor --detach-sign --output "${out}/${a}/repodata/repomd.xml.asc" "${out}/${a}/repodata/repomd.xml"
    done
    printf '%s\n' $arches > "${out}/ARCHES"
    chown_back "$out"
    rlog "rpm: ${version} for ${arches}"
    ;;
  *) rdie "usage: build-rpm.sh sign <dir> | repo <version> <dist> <out>";;
esac
