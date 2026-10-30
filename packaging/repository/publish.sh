#!/usr/bin/env bash
# publish.sh — build, sign and publish the apt, rpm and arch repositories.
#
#   publish.sh sign <dist>                           sign the .rpm files in <dist> in place
#   publish.sh build <version> <dist> <out> [channels]
#   publish.sh publish <version> <dist> [channels]   (channels: apt,rpm,arch; default all)
#
# Each channel is built in a container of its own distribution (debian,
# fedora, archlinux), so this runs on any Linux machine with docker, such as
# GitHub's ubuntu-latest.
#
# publish, per channel:
#   build     a complete generation locally (repo/<ch>/generations/<id>)
#   upload    to the download host; nothing points at it yet
#   verify    fetch it back from REPO_URL and check every signature
#   promote   swap the `current` symlink to it (rename(2): a client sees the
#             old repository or the new one, never a mix) and keep the old
#             target as `previous`
#   retain    delete old generations beyond REPO_RETAIN (default 3), never
#             a `current` or `previous` target
# A channel that fails a step leaves its published repository untouched; the
# script carries on with the other channels and exits non-zero at the end.
#
# Env: REPO_SIGNING_KEY (armored private key), REPO_SIGNING_PASSPHRASE,
#      PROFILE (REPO_URL comes from it; REPO_URL in the environment wins),
#      REPO_REMOTE_DIR (directory under DOWNLOAD_ROOT that REPO_URL serves;
#      default "repo"), REPO_PUBLIC_KEY (the key the packages ship; default
#      packaging/repository/keys/agent-keepalive.asc), REPO_ALLOW_PRERELEASE,
#      and the download host variables of packaging/remote.sh.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"
# shellcheck source=common.sh
. "${HERE}/common.sh"
# shellcheck source=../remote.sh
. "${ROOT}/packaging/remote.sh"

IMG_APT="${REPO_IMAGE_APT:-debian:stable-slim}"
IMG_RPM="${REPO_IMAGE_RPM:-fedora:latest}"
IMG_ARCH="${REPO_IMAGE_ARCH:-archlinux:latest}"

abspath() { mkdir -p "$1" && (cd "$1" && pwd); }

# in_container <image> <setup command> <script args...>
in_container() {
  local image="$1" setup="$2"; shift 2
  [ -n "${REPO_SIGNING_KEY:-}" ] || rdie "REPO_SIGNING_KEY is required"
  local mounts=(-v "${ROOT}:${ROOT}")
  local a
  for a in "$@"; do
    case "$a" in /*) [ -d "$a" ] && [[ "$a" != "${ROOT}"* ]] && mounts+=(-v "${a}:${a}");; esac
  done
  docker run --rm -i "${mounts[@]}" \
    -e REPO_SIGNING_KEY -e REPO_SIGNING_PASSPHRASE \
    -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" -e DEBIAN_FRONTEND=noninteractive \
    "$image" bash -c "set -e; ${setup}; exec \"\$@\"" bash "$@"
}

build_channel() { # build_channel <channel> <version> <dist> <out>
  local ch="$1" v="$2" dist="$3" out="$4"
  mkdir -p "$out"
  case "$ch" in
    apt)  in_container "$IMG_APT" \
            "apt-get update -qq >/dev/null && apt-get install -y -qq --no-install-recommends apt-utils gnupg >/dev/null" \
            "${HERE}/build-apt.sh" "$v" "$dist" "$out";;
    rpm)  in_container "$IMG_RPM" \
            "dnf -y -q install rpm-sign createrepo_c gnupg2 >/dev/null" \
            "${HERE}/build-rpm.sh" repo "$v" "$dist" "$out";;
    arch) in_container "$IMG_ARCH" \
            "pacman-key --init >/dev/null 2>&1 || true" \
            "${HERE}/build-arch.sh" "$v" "$dist" "$out";;
    *) rdie "unknown channel ${ch}";;
  esac
}

# The public half of REPO_SIGNING_KEY, armored, and its fingerprint.
export_public() { # export_public <outdir>
  local gh; gh="$(mktemp -d)"
  printf '%s\n' "$REPO_SIGNING_KEY" | GNUPGHOME="$gh" gpg --batch --quiet --import 2>/dev/null
  GNUPGHOME="$gh" gpg --batch --armor --export > "${1}/agent-keepalive-archive-keyring.asc"
  GNUPGHOME="$gh" gpg --batch --export > "${1}/agent-keepalive-archive-keyring.gpg"
  GNUPGHOME="$gh" gpg --batch --with-colons --list-keys \
    | awk -F: '$1=="pub"{w=1;next} $1=="fpr"&&w{print $10;w=0}' > "${1}/FINGERPRINT"
  rm -rf "$gh"
}

# gpg_verify <key.asc> <file> [detached-sig]
gpg_verify() {
  local gh rc=0; gh="$(mktemp -d)"
  GNUPGHOME="$gh" gpg --batch --quiet --import "$1" 2>/dev/null
  if [ $# -eq 3 ]; then
    GNUPGHOME="$gh" gpg --batch --quiet --verify "$3" "$2" 2>/dev/null || rc=1
  else
    GNUPGHOME="$gh" gpg --batch --quiet --verify "$2" 2>/dev/null || rc=1
  fi
  rm -rf "$gh"
  return "$rc"
}

fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
sha() { sha256sum "$1" | awk '{print $1}'; }

# verify_served <channel> <url of the generation> <key.asc> <local generation>
# The client's view (signatures check against the key) and the publisher's
# (the bytes served are the bytes just signed).
verify_served() {
  local ch="$1" url="$2" key="$3" gen="$4" t a
  t="$(mktemp -d)"
  case "$ch" in
    apt)
      fetch "${url}/dists/stable/InRelease" "${t}/InRelease" || { echo "apt: InRelease not served"; return 1; }
      gpg_verify "$key" "${t}/InRelease" || { echo "apt: InRelease does not verify"; return 1; }
      for a in $(awk -F': ' '/^Architectures:/{print $2; exit}' "${gen}/dists/stable/Release"); do
        fetch "${url}/dists/stable/main/binary-${a}/Packages" "${t}/P" || { echo "apt: Packages (${a}) not served"; return 1; }
        grep -qF "$(sha "${t}/P")" "${t}/InRelease" || { echo "apt: served Packages (${a}) is not the signed one"; return 1; }
        while read -r f; do
          fetch "${url}/${f}" "${t}/deb" || { echo "apt: ${f} not served"; return 1; }
          [ "$(sha "${t}/deb")" = "$(sha "${gen}/${f}")" ] || { echo "apt: ${f} differs"; return 1; }
        done < <(awk -F': ' '/^Filename:/{print $2}' "${t}/P")
      done;;
    rpm|arch)
      for a in $(cat "${gen}/ARCHES"); do
        local idx sig
        if [ "$ch" = rpm ]; then idx="${a}/repodata/repomd.xml" sig="${idx}.asc"
        else idx="${a}/agent-keepalive.db" sig="${idx}.sig"; fi
        fetch "${url}/${idx}" "${t}/idx" && fetch "${url}/${sig}" "${t}/sig" || { echo "${ch}: ${idx} not served"; return 1; }
        gpg_verify "$key" "${t}/idx" "${t}/sig" || { echo "${ch}: ${idx} does not verify"; return 1; }
        [ "$(sha "${t}/idx")" = "$(sha "${gen}/${idx}")" ] || { echo "${ch}: served ${idx} differs"; return 1; }
        for f in "${gen}/${a}"/*.rpm "${gen}/${a}"/*.pkg.tar.zst; do
          [ -f "$f" ] || continue
          fetch "${url}/${a}/$(basename "$f")" "${t}/pkg" || { echo "${ch}: $(basename "$f") not served"; return 1; }
          [ "$(sha "${t}/pkg")" = "$(sha "$f")" ] || { echo "${ch}: $(basename "$f") differs"; return 1; }
        done
      done;;
  esac
  rm -rf "$t"
}

promote() { # promote <channel> <generation id>
  local base="${DOWNLOAD_ROOT}/${REMOTE_DIR}/$1"
  remote_run "set -e; cd '${base}'; test -d 'generations/$2'
    if [ -L current ]; then ln -sfn \"\$(readlink current)\" previous.new && mv -Tf previous.new previous; fi
    ln -sfn 'generations/$2' current.new && mv -Tf current.new current"
}

retain() { # retain <channel>
  local base="${DOWNLOAD_ROOT}/${REMOTE_DIR}/$1" keep="${REPO_RETAIN:-3}"
  # Generation ids start with a UTC timestamp, so a name sort is a time sort.
  remote_run "set -e; cd '${base}'
    cur=\"\$(basename \"\$(readlink current 2>/dev/null)\" 2>/dev/null)\"
    prev=\"\$(basename \"\$(readlink previous 2>/dev/null)\" 2>/dev/null)\"
    ls -1 generations | sort -r | tail -n +$((keep + 1)) | while read -r g; do
      [ \"\$g\" = \"\$cur\" ] || [ \"\$g\" = \"\$prev\" ] || rm -rf -- \"generations/\$g\"
    done"
}

setup_files() { # setup_files <dir> <repo url>
  local d="$1" url="$2"
  mkdir -p "$d"
  export_public "$d"
  cat > "${d}/agent-keepalive.sources" <<EOF
Types: deb
URIs: ${url}/apt/current
Suites: stable
Components: main
Architectures: amd64 arm64
Signed-By: /usr/share/keyrings/agent-keepalive-archive-keyring.gpg
EOF
  echo "deb [arch=amd64,arm64 signed-by=/usr/share/keyrings/agent-keepalive-archive-keyring.gpg] ${url}/apt/current stable main" \
    > "${d}/agent-keepalive.list"
  cat > "${d}/agent-keepalive.repo" <<EOF
[agent-keepalive]
name=agent-keepalive
baseurl=${url}/rpm/current/\$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=${url}/agent-keepalive-archive-keyring.asc
metadata_expire=6h
EOF
  cat > "${d}/pacman.conf" <<EOF
[agent-keepalive]
SigLevel = Required DatabaseRequired
Server = ${url}/arch/current/\$arch
EOF
}

check_shipped_key() {
  local shipped="${REPO_PUBLIC_KEY:-${HERE}/keys/agent-keepalive.asc}" t
  t="$(mktemp -d)"; export_public "$t"
  if ! grep -q "BEGIN PGP PUBLIC KEY BLOCK" "$shipped" 2>/dev/null; then
    rlog "WARNING ${shipped#"${ROOT}"/} is missing: packages from this tree register no repository"
  else
    local fpr; fpr="$(gpg --batch --with-colons --show-keys "$shipped" 2>/dev/null | awk -F: '$1=="fpr"{print $10; exit}')"
    # Packages that ship a different key register a repository they cannot verify.
    grep -qx "$fpr" "${t}/FINGERPRINT" \
      || rdie "${shipped#"${ROOT}"/} (${fpr}) is not the public half of REPO_SIGNING_KEY ($(cat "${t}/FINGERPRINT"))"
  fi
  rm -rf "$t"
}

channels_of() { printf '%s\n' "${1:-apt,rpm,arch}" | tr ',' ' '; }

cmd="${1:-}"; shift || true
case "$cmd" in
  sign)
    dist="$(abspath "${1:?usage: publish.sh sign <dist>}")"
    compgen -G "${dist}/*.rpm" >/dev/null || { rlog "no .rpm files to sign"; exit 0; }
    in_container "$IMG_RPM" "dnf -y -q install rpm-sign gnupg2 >/dev/null" "${HERE}/build-rpm.sh" sign "$dist"
    ;;
  build)
    v="${1:?usage: publish.sh build <version> <dist> <out> [channels]}"
    dist="$(abspath "$2")" out="$(abspath "$3")"
    for ch in $(channels_of "${4:-}"); do build_channel "$ch" "$v" "$dist" "${out}/${ch}"; done
    ;;
  publish)
    v="${1:?usage: publish.sh publish <version> <dist> [channels]}"
    dist="$(abspath "$2")"
    case "$v" in *-*)
      [ "${REPO_ALLOW_PRERELEASE:-0}" = 1 ] || rdie "refusing to publish pre-release ${v} to the stable repositories";;
    esac
    remote_check
    REMOTE_DIR="${REPO_REMOTE_DIR:-repo}"
    url="${REPO_URL:-$("${ROOT}/scripts/profile.sh" get REPO_URL)}"
    url="${url%/}"
    [ -n "$url" ] || rdie "no REPO_URL (profile or environment)"
    check_shipped_key
    work="${ROOT}/bin/repo"; rm -rf "$work"; mkdir -p "$work"
    gen="$(date -u +%Y%m%dT%H%M%SZ)-${v}"

    # Setup files and keys first: they are additive and the same for every release.
    setup_files "${work}/setup" "$url"
    remote_put "$REMOTE_DIR" "${work}/setup/"

    failed=""
    for ch in $(channels_of "${3:-}"); do
      out="${work}/${ch}/${gen}"
      if ! build_channel "$ch" "$v" "$dist" "$out"; then failed="${failed} ${ch}"; continue; fi
      if ! remote_put "${REMOTE_DIR}/${ch}/generations/${gen}" "${out}/"; then
        rlog "${ch}: upload failed"; failed="${failed} ${ch}"; continue
      fi
      if ! err="$(verify_served "$ch" "${url}/${ch}/generations/${gen}" "${work}/setup/agent-keepalive-archive-keyring.asc" "$out" 2>&1)"; then
        rlog "${ch}: verification failed, current left as it was: ${err}"; failed="${failed} ${ch}"; continue
      fi
      promote "$ch" "$gen" || { rlog "${ch}: promote failed"; failed="${failed} ${ch}"; continue; }
      retain "$ch" || rlog "${ch}: WARNING retention failed (the release is live)"
      rlog "${ch}: ${v} published as ${gen}"
    done
    [ -z "$failed" ] || rdie "failed channels:${failed}"
    ;;
  *) rdie "usage: publish.sh sign <dist> | build <version> <dist> <out> [channels] | publish <version> <dist> [channels]";;
esac
