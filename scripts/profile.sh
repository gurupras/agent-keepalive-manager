#!/usr/bin/env bash
# profile.sh — read a distribution profile (profiles/*.env) and turn it into
# link-time flags for internal/distro.
#
#   scripts/profile.sh ldflags [--kind KIND] [--version V]   print -ldflags
#   scripts/profile.sh get KEY                               print one value
#   scripts/profile.sh check [--release]                     validate
#   scripts/profile.sh show                                  print KEY=value
#
# The profile is $PROFILE (a path). With no PROFILE the build is a dev build:
# no -X flags for the profile at all, so the binary has no metrics endpoint and
# no update feed and never contacts anything.
#
# The file is parsed, never sourced: it is KEY=value lines, '#' comments and
# blank lines, values optionally wrapped in one pair of quotes. Unknown keys
# are an error, so a typo cannot silently drop a setting from a build.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="$(awk '$1=="module"{print $2; exit}' "${ROOT}/go.mod")"
DISTRO="${MODULE}/internal/distro"

# profile key -> internal/distro variable. (A function, not an associative
# array: macOS still ships bash 3.2.)
var_of() {
  case "$1" in
    PROFILE_NAME) echo name;;
    VENDOR) echo vendor;;
    HOMEPAGE) echo homepage;;
    METRICS_URL) echo metricsURL;;
    METRICS_DEFAULT) echo metricsDefault;;
    METRICS_LOCKED) echo metricsLocked;;
    UPDATE_FEED_URL) echo updateFeedURL;;
    UPDATE_PUBLIC_KEY) echo updatePublicKey;;
    DOWNLOAD_URL) echo downloadURL;;
    REPO_URL) echo repoURL;;
    *) return 1;;
  esac
}
# Emitted in this order so the flags are stable between runs.
ORDER="PROFILE_NAME VENDOR HOMEPAGE METRICS_URL METRICS_DEFAULT METRICS_LOCKED
       UPDATE_FEED_URL UPDATE_PUBLIC_KEY DOWNLOAD_URL REPO_URL"
KINDS=" dev archive deb rpm arch brew appimage windows-setup windows-portable macos-pkg "

die() { echo "profile.sh: $*" >&2; exit 1; }

load() {
  local file="${PROFILE:-}" line key val n=0
  [ -n "$file" ] || return 0
  [ -f "$file" ] || die "PROFILE=${file}: no such file"
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    line="${line%$'\r'}"
    case "$line" in ''|'#'*) continue;; esac
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ "$line" =~ ^[[:space:]]*$ ]] && continue
    [[ "$line" =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]] || die "${file}:${n}: not KEY=value: ${line}"
    key="${BASH_REMATCH[1]}"
    val="${BASH_REMATCH[2]}"
    var_of "$key" >/dev/null || die "${file}:${n}: unknown key ${key}"
    if [[ "$val" =~ ^\"(.*)\"$ ]] || [[ "$val" =~ ^\'(.*)\'$ ]]; then
      val="${BASH_REMATCH[1]}"
    fi
    case "$val" in *"'"*|*'"'*|*'\'*) die "${file}:${n}: ${key} may not contain quotes or backslashes";; esac
    eval "P_${key}=\$val; P_${key}_set=1"
  done < "$file"
}

# Values live in P_<KEY> (P_<KEY>_set marks presence); keys are checked
# against var_of before they get here, so the eval only ever sees [A-Z0-9_].
has() { eval "[ -n \"\${P_${1}_set:-}\" ]"; }
get() { eval "printf '%s' \"\${P_${1}:-}\""; }

check() {
  local release=0
  [ "${1:-}" = "--release" ] && release=1
  [ -n "${PROFILE:-}" ] || { [ "$release" -eq 0 ] || die "a release needs PROFILE"; return 0; }
  local v
  v="$(get METRICS_DEFAULT)"; case "$v" in ''|on|off) ;; *) die "METRICS_DEFAULT must be on or off (got ${v})";; esac
  v="$(get METRICS_LOCKED)"; case "$v" in ''|true|false) ;; *) die "METRICS_LOCKED must be true or false (got ${v})";; esac
  for k in HOMEPAGE METRICS_URL UPDATE_FEED_URL DOWNLOAD_URL REPO_URL; do
    v="$(get "$k")"
    [ -z "$v" ] || [[ "$v" =~ ^https?://[^[:space:]]+$ ]] || die "${k} is not a URL: ${v}"
  done
  v="$(get UPDATE_PUBLIC_KEY)"
  [ -z "$v" ] || [[ "$v" =~ ^[0-9a-fA-F]{64}$ ]] || die "UPDATE_PUBLIC_KEY must be 64 hex characters (an Ed25519 public key)"
  if [ -n "$(get UPDATE_FEED_URL)" ] && [ -z "$v" ]; then
    # The updater refuses to run with a feed but no key, so such a build has
    # no working self-update. Fine for a local try-out, wrong for a release.
    if [ "$release" -eq 1 ]; then
      die "UPDATE_FEED_URL is set but UPDATE_PUBLIC_KEY is empty: run 'go run ./tools/release keygen' and put the public key in ${PROFILE}"
    fi
    echo "profile.sh: warning: UPDATE_FEED_URL without UPDATE_PUBLIC_KEY; self-update will be disabled in this build" >&2
  fi
}

ldflags() {
  local kind="dev" version=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --kind) kind="$2"; shift 2;;
      --version) version="$2"; shift 2;;
      *) die "ldflags: unknown argument $1";;
    esac
  done
  case "$KINDS" in *" ${kind} "*) ;; *) die "unknown install kind ${kind}";; esac
  [ -n "$version" ] || version="$("${ROOT}/scripts/version.sh")"
  local out=(-s -w "-X main.version=${version}" "-X ${DISTRO}.installKind=${kind}")
  # The Windows installs are GUI-subsystem programs, so starting one from the
  # Start menu or a double-click opens no console window (the program attaches
  # to its parent's console when run from a terminal).
  case "$kind" in windows-setup|windows-portable) out+=(-H windowsgui);; esac
  local k
  for k in $ORDER; do
    has "$k" || continue
    out+=("-X '${DISTRO}.$(var_of "$k")=$(get "$k")'")
  done
  printf '%s\n' "${out[*]}"
}

load
cmd="${1:-}"; shift || true
case "$cmd" in
  ldflags) check; ldflags "$@";;
  get)     [ $# -eq 1 ] || die "usage: get KEY"; var_of "$1" >/dev/null || die "unknown key $1"; get "$1"; echo;;
  check)   check "$@";;
  show)    for k in $ORDER; do has "$k" && printf '%s=%s\n' "$k" "$(get "$k")"; done; true;;
  *)       die "usage: profile.sh ldflags|get|check|show";;
esac
