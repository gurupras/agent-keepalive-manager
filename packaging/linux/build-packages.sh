#!/usr/bin/env bash
# build-packages.sh — build the deb, rpm and archlinux packages.
#
#   packaging/linux/build-packages.sh [deb] [rpm] [arch]     (default: all three)
#
# Env: PROFILE (distribution profile), VERSION, ARCHES ("amd64 arm64"),
#      DIST (output dir), REPO_PUBLIC_KEY (armored repository public key; the
#      default is packaging/repository/keys/agent-keepalive.asc),
#      PACKAGE_MAINTAINER.
#
# Each package carries a binary linked with its own install kind, and a
# postinstall that registers the profile's REPO_URL repository with the key
# shipped in the package.
set -euo pipefail
LOG_TAG=linux
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
. "${HERE}/../lib.sh"

command -v nfpm >/dev/null 2>&1 || die "nfpm not found: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest"

formats=("$@")
[ ${#formats[@]} -gt 0 ] || formats=(deb rpm arch)

repo_url="$(profile_get REPO_URL)"
vendor="$(profile_get VENDOR)"
homepage="$(profile_get HOMEPAGE)"
maintainer="${PACKAGE_MAINTAINER:-${vendor:-agent-keepalive} <gurupras@users.noreply.github.com>}"
key="${REPO_PUBLIC_KEY:-${PKG_ROOT}/packaging/repository/keys/agent-keepalive.asc}"

mkdir -p "$DIST"
for fmt in "${formats[@]}"; do
  case "$fmt" in
    deb) packager=deb;; rpm) packager=rpm;; arch) packager=archlinux;;
    *) die "unknown format ${fmt} (deb, rpm, arch)";;
  esac
  for goarch in $(arches); do
    bin="$(ensure_bin "$fmt" linux "$goarch")"
    stage="${PKG_ROOT}/bin/stage/${fmt}_${goarch}"
    rm -rf "$stage"; mkdir -p "$stage"

    install -m 0755 "$bin" "${stage}/agent-keepalive"
    install -m 0644 "${HERE}/agent-keepalive.desktop" "${HERE}/agent-keepalive.service" \
      "${HERE}/nfpm.yaml" "$stage/"
    install -m 0644 "${PKG_ROOT}/packaging/icons/agent-keepalive.svg" \
      "${PKG_ROOT}/packaging/icons/agent-keepalive.png" "$stage/"
    install -m 0644 "${PKG_ROOT}/LICENSE" "$stage/"

    if grep -q "BEGIN PGP PUBLIC KEY BLOCK" "$key" 2>/dev/null; then
      install -m 0644 "$key" "${stage}/repository-key.asc"
      gpg --batch --with-colons --show-keys "$key" 2>/dev/null \
        | awk -F: '$1=="pub"{want=1;next} $1=="fpr"&&want{print $10;want=0}' > "${stage}/repository-fingerprint"
    else
      [ -z "$repo_url" ] || log "no repository key at ${key#"${PKG_ROOT}"/}: the ${fmt} package will register no repository"
      echo "no repository key in this build" > "${stage}/repository-key.asc"
      : > "${stage}/repository-fingerprint"
    fi

    for s in postinstall postremove; do
      install -m 0755 "${HERE}/${s}.sh.in" "${stage}/${s}.sh"
      subst "${stage}/${s}.sh" "KIND=${fmt}" "REPO_URL=${repo_url}"
      chmod 0755 "${stage}/${s}.sh"
    done
    if [ "$fmt" = arch ]; then
      pkgversion="${VERSION//-/}" schema=none
    else
      pkgversion="$VERSION" schema=semver
    fi
    subst "${stage}/nfpm.yaml" "PKGVERSION=${pkgversion}" "SCHEMA=${schema}" "ARCH=${goarch}" \
      "MAINTAINER=${maintainer}" "VENDOR=${vendor:-agent-keepalive}" \
      "HOMEPAGE=${homepage:-https://github.com/gurupras/agent-keepalive-manager}"

    case "$fmt" in
      deb)  name="agent-keepalive_${VERSION}_${goarch}.deb";;
      rpm)  name="agent-keepalive-${VERSION}-1.$(uname_arch "$goarch").rpm";;
      arch) name="agent-keepalive-${VERSION}-1-$(uname_arch "$goarch").pkg.tar.zst";;
    esac
    ( cd "$stage" && nfpm package --config nfpm.yaml --packager "$packager" --target "${DIST}/${name}" >/dev/null )
    [ -s "${DIST}/${name}" ] || die "nfpm produced no ${name}"
    log "${name}"
  done
done
