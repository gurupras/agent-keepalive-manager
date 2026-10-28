# Release keys

The private halves of the release keys, encrypted with
[git-crypt](https://github.com/AGWA/git-crypt). Without the git-crypt key
these files are opaque; CI never needs them (it reads the same keys from
GitHub secrets). They are here so the keys survive the loss of any one
machine or of the GitHub secrets.

| File | Key | GitHub secret | Public half |
|---|---|---|---|
| `release-upload-key` | ssh key the release workflow uploads with | `DOWNLOAD_SSH_KEY` | `release-upload-key.pub` in the upload container's `authorized_keys` |
| `update-signing-key` | Ed25519 seed that signs `latest.json` | `UPDATE_SIGNING_KEY` | `UPDATE_PUBLIC_KEY` in `profiles/public.env` |
| `repo-signing-key.asc` | GPG key that signs the apt/rpm/arch repositories | `REPO_SIGNING_KEY` | `packaging/repository/keys/agent-keepalive.asc` |

Unlock a clone with `git-crypt unlock` (needs a GPG key added with
`git-crypt add-gpg-user`). Do not unlock on the download host: it needs
none of these, and keeping the signing keys off it is what stops a
compromised host from forging updates.
