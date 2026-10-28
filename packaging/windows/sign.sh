#!/usr/bin/env bash
# sign.sh <file.exe> — Authenticode-sign a Windows executable in place, if a
# certificate is configured; otherwise say so and leave the file unsigned.
#
# Env (all optional; signing happens only when a certificate is given):
#   WINDOWS_SIGN_PFX           path to a .pfx/.p12 code-signing certificate
#   WINDOWS_SIGN_PFX_BASE64    the same file, base64 (how CI secrets carry it)
#   WINDOWS_SIGN_PASSWORD      its password
#   WINDOWS_SIGN_TIMESTAMP_URL RFC 3161 timestamp server (default: DigiCert's)
#
# Uses osslsigncode, so it runs on Linux.
set -euo pipefail
f="${1:?usage: sign.sh <file.exe>}"

if [ -z "${WINDOWS_SIGN_PFX:-}" ] && [ -z "${WINDOWS_SIGN_PFX_BASE64:-}" ]; then
  echo "[sign] Authenticode signing skipped for $(basename "$f"): no WINDOWS_SIGN_PFX configured" >&2
  exit 0
fi
command -v osslsigncode >/dev/null 2>&1 || { echo "[sign] osslsigncode not found (apt install osslsigncode)" >&2; exit 1; }

pfx="${WINDOWS_SIGN_PFX:-}"
tmp=""
if [ -z "$pfx" ]; then
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  pfx="${tmp}/cert.pfx"
  printf '%s' "$WINDOWS_SIGN_PFX_BASE64" | base64 -d > "$pfx"
fi

osslsigncode sign -pkcs12 "$pfx" -pass "${WINDOWS_SIGN_PASSWORD:-}" \
  -n "agent-keepalive" -i "https://github.com/gurupras/agent-keepalive-manager" \
  -h sha256 -ts "${WINDOWS_SIGN_TIMESTAMP_URL:-http://timestamp.digicert.com}" \
  -in "$f" -out "${f}.signed" >/dev/null
mv -f "${f}.signed" "$f"
echo "[sign] signed $(basename "$f")" >&2
