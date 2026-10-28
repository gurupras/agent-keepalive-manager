#!/usr/bin/env bash
# common.sh — shared by the repository scripts. Sourced, not run.

rlog() { printf '[repo] %s\n' "$*" >&2; }
rdie() { printf '[repo] ERROR %s\n' "$*" >&2; exit "${RDIE_CODE:-1}"; }

# gpg_init: import REPO_SIGNING_KEY (an armored private key) into a fresh,
# private GNUPGHOME and set REPO_KEY_FPR to its primary fingerprint.
# REPO_SIGNING_PASSPHRASE is used when the key has one.
gpg_init() {
  [ -n "${REPO_SIGNING_KEY:-}" ] || rdie "REPO_SIGNING_KEY (armored private key) is required"
  GNUPGHOME="$(mktemp -d)"
  export GNUPGHOME
  chmod 700 "$GNUPGHOME"
  printf 'allow-loopback-pinentry\n' > "${GNUPGHOME}/gpg-agent.conf"
  printf '%s\n' "$REPO_SIGNING_KEY" | gpg --batch --quiet --import 2>/dev/null \
    || rdie "REPO_SIGNING_KEY could not be imported"
  REPO_KEY_FPR="$(gpg --batch --with-colons --list-secret-keys \
    | awk -F: '$1=="sec"{want=1;next} $1=="fpr"&&want{print $10;exit}')"
  [ -n "$REPO_KEY_FPR" ] || rdie "REPO_SIGNING_KEY holds no secret key"
  export REPO_KEY_FPR
  rlog "signing key ${REPO_KEY_FPR}"
}

# rgpg <gpg args>: gpg, non-interactive, signing with the repository key.
rgpg() {
  if [ -n "${REPO_SIGNING_PASSPHRASE:-}" ]; then
    gpg --batch --yes --pinentry-mode loopback --passphrase "$REPO_SIGNING_PASSPHRASE" \
      --local-user "$REPO_KEY_FPR" "$@"
  else
    gpg --batch --yes --pinentry-mode loopback --local-user "$REPO_KEY_FPR" "$@"
  fi
}

# Host files written inside a container belong to root; hand them back.
chown_back() { [ -n "${HOST_UID:-}" ] && chown -R "${HOST_UID}:${HOST_GID:-$HOST_UID}" "$@" 2>/dev/null; return 0; }
