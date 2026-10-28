#!/usr/bin/env bash
# remote.sh — ssh/rsync access to the download host. Sourced by upload-ssh.sh
# and packaging/repository/publish.sh.
#
# Env:
#   DOWNLOAD_SSH_HOST         host name or address                       (required)
#   DOWNLOAD_SSH_USER         login user                                 (required)
#   DOWNLOAD_ROOT             absolute directory nginx serves at /download (required)
#   DOWNLOAD_SSH_KEY_FILE     private key file (default: ssh's own)
#   DOWNLOAD_SSH_KNOWN_HOSTS_FILE  known_hosts with the host's key; without it
#                             the connection is refused (no trust on first use)
#   DOWNLOAD_SSH_PORT         default 22
#
# With DOWNLOAD_LOCAL=1 the "host" is this machine: DOWNLOAD_ROOT is a local
# directory and no ssh is used (for testing the scripts end to end).

remote_check() {
  : "${DOWNLOAD_ROOT:?DOWNLOAD_ROOT is required}"
  case "$DOWNLOAD_ROOT" in /*) ;; *) echo "remote: DOWNLOAD_ROOT must be absolute" >&2; return 64;; esac
  [ "${DOWNLOAD_LOCAL:-0}" = 1 ] && return 0
  : "${DOWNLOAD_SSH_HOST:?DOWNLOAD_SSH_HOST is required}"
  : "${DOWNLOAD_SSH_USER:?DOWNLOAD_SSH_USER is required}"
}

remote_ssh_cmd() {
  local c="ssh -o BatchMode=yes -o ConnectTimeout=20 -p ${DOWNLOAD_SSH_PORT:-22}"
  if [ -n "${DOWNLOAD_SSH_KNOWN_HOSTS_FILE:-}" ]; then
    c="${c} -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${DOWNLOAD_SSH_KNOWN_HOSTS_FILE}"
  else
    c="${c} -o StrictHostKeyChecking=yes"
  fi
  [ -n "${DOWNLOAD_SSH_KEY_FILE:-}" ] && c="${c} -i ${DOWNLOAD_SSH_KEY_FILE} -o IdentitiesOnly=yes"
  printf '%s' "$c"
}

# remote_run <shell command> — run on the host (or locally with DOWNLOAD_LOCAL=1).
remote_run() {
  if [ "${DOWNLOAD_LOCAL:-0}" = 1 ]; then
    sh -c "$1"
  else
    # shellcheck disable=SC2046  # the ssh command is a deliberate word list
    $(remote_ssh_cmd) -n "${DOWNLOAD_SSH_USER}@${DOWNLOAD_SSH_HOST}" "$1"
  fi
}

# remote_put <dest dir relative to DOWNLOAD_ROOT> <src>... — copy files or
# directory contents (a trailing / on a directory copies its contents).
remote_put() {
  local dest="$1"; shift
  case "$dest" in /*|*..*) echo "remote: refusing destination ${dest}" >&2; return 64;; esac
  remote_run "mkdir -p '${DOWNLOAD_ROOT}/${dest}'"
  # --chmod: whatever the umask here, nginx must be able to read the files.
  if [ "${DOWNLOAD_LOCAL:-0}" = 1 ]; then
    rsync -rlt --chmod=D755,F644 "$@" "${DOWNLOAD_ROOT}/${dest}/"
  else
    rsync -rlt --chmod=D755,F644 -e "$(remote_ssh_cmd)" "$@" \
      "${DOWNLOAD_SSH_USER}@${DOWNLOAD_SSH_HOST}:${DOWNLOAD_ROOT}/${dest}/"
  fi
}
