#!/usr/bin/env bash
# gen-repo-key.sh — make the GPG key that signs the apt/rpm/arch repositories.
#
#   scripts/gen-repo-key.sh [outdir]      (default: a new directory under /tmp)
#
# Writes
#   <outdir>/repo-signing-key.asc   PRIVATE: paste into the REPO_SIGNING_KEY
#                                   secret, keep an offline copy, then delete it
#   <outdir>/agent-keepalive.asc    public: commit as packaging/repository/keys/agent-keepalive.asc
#   <outdir>/FINGERPRINT
#
# RSA 4096, which every rpm, apt and pacman version verifies. The key has no passphrase (CI signs unattended) and expires in 5 years;
# extend it before then with gpg --quick-set-expire and re-commit the public key.
# Env: REPO_KEY_NAME (default "agent-keepalive repository"),
#      REPO_KEY_EMAIL (default repository@agent-keepalive.invalid).
set -euo pipefail
out="${1:-$(mktemp -d /tmp/agent-keepalive-repo-key.XXXXXX)}"
mkdir -p "$out"
name="${REPO_KEY_NAME:-agent-keepalive repository}"
email="${REPO_KEY_EMAIL:-repository@agent-keepalive.invalid}"

GNUPGHOME="$(mktemp -d)"; export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT
gpg --batch --quiet --passphrase '' --quick-generate-key "${name} <${email}>" rsa4096 sign 5y
fpr="$(gpg --batch --with-colons --list-keys | awk -F: '$1=="fpr"{print $10; exit}')"
gpg --batch --armor --export-secret-keys "$fpr" > "${out}/repo-signing-key.asc"
gpg --batch --armor --export "$fpr" > "${out}/agent-keepalive.asc"
echo "$fpr" > "${out}/FINGERPRINT"
chmod 600 "${out}/repo-signing-key.asc"
cat >&2 <<EOF
Repository key ${fpr}
  private: ${out}/repo-signing-key.asc   -> GitHub secret REPO_SIGNING_KEY
  public:  ${out}/agent-keepalive.asc    -> packaging/repository/keys/agent-keepalive.asc (commit it)
EOF
