# Releasing

A release is a `v*` tag. `.github/workflows/release.yml` does the rest.

## How a release flows

```
tag v1.2.0
  meta          version 1.2.0, prerelease = tag has a hyphen
  ui            npm ci && npm run build for web/ and hub/web/ (one artifact, reused below)
  linux         profile check; per install kind and target: go build with -X flags from the profile;
                deb/rpm/arch (nfpm), AppImage x86_64/aarch64 (appimagetool), Windows setup (NSIS)
                and portable exe x64/arm64, archives, hub archives; rpm signing; install the .deb in
                a container and run `version`
  macos         the .pkg for arm64 and x64 (pkgbuild/productbuild; signed and notarized when the
                Apple secrets are set)
  windows-smoke on windows-latest: portable `version`, `serve` until api/health answers, `stop`;
                setup /S, the installed exe runs, uninstall /S
  hub-image     ghcr.io/gurupras/agent-keepalive-hub:1.2.0 (+ :latest for stable), amd64 + arm64
  publish       SHA256SUMS; latest.json signed with UPDATE_SIGNING_KEY; build provenance
                attestations; the GitHub Release (notes = the tag's annotation); rsync to
                <DOWNLOAD_ROOT>/1.2.0/; [stable only] apt/rpm/arch repositories, Homebrew tap,
                and last of all <DOWNLOAD_ROOT>/latest.json
```

Until `latest.json` moves, no installed copy knows about the release, so a failure anywhere before that step leaves users where they were. A pre-release (`v1.2.0-rc.1`) is published to GitHub (marked pre-release), uploaded to its version folder and pushed as a hub image tag, and stops there.

**Dry run**: Actions › Release › Run workflow. It builds and tests everything with the version from `scripts/version.sh`, signs the feed when `UPDATE_SIGNING_KEY` is available (and skips the feed otherwise: the workflow never writes an unsigned one), publishes nothing, and keeps the output as the `release-dry-run` artifact.

## Checklist

1. CI is green on `main`.
2. Dry-run the release workflow if packaging or the workflow changed.
3. Write the notes and tag (the annotation becomes the GitHub Release notes and the feed's `notes`):
   ```sh
   git tag -a v1.2.0 -m "agent-keepalive 1.2.0

   - What changed, for users."
   git push origin v1.2.0
   ```
4. Watch the workflow. Afterwards:
   ```sh
   curl -s https://agent-keepalive.gurupras.me/download/latest.json | jq .version
   go run ./tools/release verify -key "$(scripts/profile.sh get UPDATE_PUBLIC_KEY)" latest.json   # optional
   docker run --rm debian:stable-slim sh -c 'apt-get update -qq && apt-get install -y -qq curl >/dev/null && …'  # apt install from the repository
   brew update && brew upgrade agent-keepalive
   ```
5. Update the hub on the host: `docker compose pull hub && docker compose up -d hub` (see [deploy/README.md](../deploy/README.md)).

## One-time setup

### Keys

```sh
go run ./tools/release keygen          # prints an Ed25519 pair (hex)
```

- The **public** key goes in `profiles/public.env` as `UPDATE_PUBLIC_KEY` and is committed. A release refuses to build without it (`scripts/profile.sh check --release`), because the updater refuses a feed URL without a key.
- The **private** seed is the `UPDATE_SIGNING_KEY` secret.

```sh
scripts/gen-repo-key.sh                # the repository GPG key
```

- `repo-signing-key.asc` → the `REPO_SIGNING_KEY` secret (keep an offline copy, then delete the file).
- `agent-keepalive.asc` → commit as `packaging/repository/keys/agent-keepalive.asc`. The Linux packages ship it.

### The download host

DNS, the host (nginx, the hub and its database), the upload key: [deploy/README.md](../deploy/README.md).

### The Homebrew tap

Create the public repository `gurupras/homebrew-tap` with a `main` branch (an empty `Formula/` is fine). Create a fine-grained personal access token with **Contents: read and write** on that repository only, and store it as `HOMEBREW_TAP_TOKEN`.

### GitHub repository settings

- Settings › Actions › General › Workflow permissions: read (the jobs ask for what they need).
- The hub image is pushed with `GITHUB_TOKEN`; after the first release, make the `agent-keepalive-hub` package public (Packages › Package settings) and link it to the repository.

## Secrets and variables

| Name | Kind | Needed for | Value |
|---|---|---|---|
| `UPDATE_SIGNING_KEY` | secret | every published release | hex Ed25519 seed from `go run ./tools/release keygen` |
| `REPO_SIGNING_KEY` | secret | rpm signing, repositories | armored private GPG key from `scripts/gen-repo-key.sh` |
| `REPO_SIGNING_PASSPHRASE` | secret | optional | only if the key has a passphrase (not wired into the workflow by default) |
| `DOWNLOAD_SSH_HOST` | secret | upload, repositories, latest.json | `agent-keepalive.gurupras.me` (or the droplet's IP) |
| `DOWNLOAD_SSH_PORT` | secret | " | the upload container's port, `2222` (default 22) |
| `DOWNLOAD_SSH_USER` | secret | " | `upload`, the upload container's only user |
| `DOWNLOAD_SSH_KEY` | secret | " | the release key's private half (ed25519, no passphrase) |
| `DOWNLOAD_SSH_KNOWN_HOSTS` | secret | " | `ssh-keyscan -p 2222 -t ed25519 <host>` output, checked against the fingerprint the container logs |
| `DOWNLOAD_ROOT` | secret | " | `/download`: the download directory as the upload container sees it |
| `HOMEBREW_TAP_TOKEN` | secret | Homebrew | token with contents write on `gurupras/homebrew-tap` |
| `WINDOWS_SIGN_PFX_BASE64`, `WINDOWS_SIGN_PASSWORD` | secret | optional | Authenticode certificate (`base64 -w0 cert.pfx`) and its password; unsigned without |
| `MACOS_CERTS_P12`, `MACOS_CERTS_PASSWORD` | secret | optional | base64 `.p12` holding the Developer ID Application and Installer identities |
| `MACOS_APP_IDENTITY`, `MACOS_INSTALLER_IDENTITY` | secret | optional | e.g. `Developer ID Application: Name (TEAMID)` |
| `MACOS_NOTARY_APPLE_ID`, `MACOS_NOTARY_TEAM_ID`, `MACOS_NOTARY_PASSWORD` | secret | optional | notarytool credentials (app-specific password) |
| `ORG_PROFILE` | secret | organisation builds | the whole content of a profile file; overrides `PROFILE_FILE` |
| `PROFILE_FILE` | variable | organisation builds | path of the profile in the checkout; default `profiles/public.env` |
| `HOMEBREW_TAP_REPO` | variable | optional | tap repository, default `gurupras/homebrew-tap` |
| `SITE_ROOT` | variable | docs site | `/site`: the site directory as the upload container sees it. The docs workflow (`docs.yml`) uploads the built site there with the `DOWNLOAD_SSH_*` secrets; unset, it only builds |

Every optional step logs that it was skipped and why. Missing download-host secrets skip the upload, the repositories and `latest.json` (the GitHub Release is still made).

## Organisation builds

An organisation keeps the public source and supplies its own profile, so none of its addresses appear in this repository:

- **A fork or an internal mirror running this workflow**: put the profile's content in the `ORG_PROFILE` secret (or commit it in the fork and set the `PROFILE_FILE` variable), and set the secrets above for its own keys and host. Its `UPDATE_PUBLIC_KEY`, `METRICS_URL`, `DOWNLOAD_URL` and `REPO_URL` point at its own infrastructure; its packages ship its own repository key (`REPO_PUBLIC_KEY`, or replace `packaging/repository/keys/agent-keepalive.asc` in the fork).
- **Another CI system**: run the same targets, `PROFILE=/path/to/org.env make dist`, on Linux and macOS, then `packaging/checksums.sh`, `go run ./tools/release feed …`, `packaging/upload-ssh.sh` and `packaging/repository/publish.sh` with its own environment. See [self-hosting.md](self-hosting.md).

## Doing it by hand

Everything the workflow runs is a script:

```sh
export PROFILE=profiles/public.env VERSION=1.2.0
make ui && make dist                       # Linux: everything but the .pkg
make macos-pkg                             # on a Mac
packaging/repository/publish.sh sign dist  # REPO_SIGNING_KEY
packaging/checksums.sh
UPDATE_SIGNING_KEY=… go run ./tools/release feed -version 1.2.0 \
  -base-url https://agent-keepalive.gurupras.me/download/1.2.0 -notes notes.md \
  -page https://github.com/gurupras/agent-keepalive-manager/releases/tag/v1.2.0 \
  -out dist/latest.json $(ls dist/* | grep -v SHA256SUMS)
packaging/upload-ssh.sh release 1.2.0 dist
packaging/repository/publish.sh publish 1.2.0 dist
packaging/upload-ssh.sh latest dist/latest.json
```

with the `DOWNLOAD_*` variables of `packaging/remote.sh` (`DOWNLOAD_SSH_KEY_FILE` and `DOWNLOAD_SSH_KNOWN_HOSTS_FILE` are file paths there).
