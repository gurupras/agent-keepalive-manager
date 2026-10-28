# Package repositories

Signed apt, rpm and pacman repositories are served under the profile's `REPO_URL` (public build: `https://agent-keepalive.gurupras.me/download/repo`). Package managers upgrade agent-keepalive from them; the program itself never replaces a packaged binary.

## Layout

```
repo/
  agent-keepalive-archive-keyring.asc   the public key, armored (dnf gpgkey=, pacman-key --add)
  agent-keepalive-archive-keyring.gpg   the same key, binary (apt Signed-By)
  FINGERPRINT
  agent-keepalive.sources               apt deb822 entry
  agent-keepalive.list                  apt one-line entry, for older apt
  agent-keepalive.repo                  dnf/zypper entry
  pacman.conf                           pacman.conf section
  apt/  rpm/  arch/
    generations/<UTC stamp>-<version>/  complete, immutable repository states
    current  -> generations/…           what clients read
    previous -> generations/…
```

| Channel | Inside a generation | Signatures checked by the client |
|---|---|---|
| apt | `dists/stable/{InRelease,Release,Release.gpg}`, `dists/stable/main/binary-{amd64,arm64}/Packages[.gz]`, `pool/main/a/agent-keepalive/*.deb` | `InRelease` (it carries the hash of every `Packages`, which carries the hash of every `.deb`) |
| rpm | `{x86_64,aarch64}/*.rpm`, `…/repodata/repomd.xml{,.asc}` | `repomd.xml.asc` (`repo_gpgcheck=1`) and each package's own signature (`gpgcheck=1`) |
| arch | `{x86_64,aarch64}/*.pkg.tar.zst{,.sig}`, `agent-keepalive.{db,files}{,.sig}` | database and package (`SigLevel = Required DatabaseRequired`) |

## How a release is published

`packaging/repository/publish.sh publish <version> dist` (run by the release workflow for stable versions only; pre-releases are refused):

1. Builds each channel in a container of its own distribution (`debian:stable-slim` with apt-ftparchive, `fedora` with rpmsign and createrepo_c, `archlinux` with repo-add), signing with `REPO_SIGNING_KEY`.
2. Uploads the generation next to the live one; nothing points at it yet.
3. Fetches it back over HTTPS and checks every signature and that the served bytes are the ones signed.
4. Promotes: `current` is replaced with a rename of a new symlink, so a client sees the old repository or the new one, never a mix. The old target becomes `previous`.
5. Deletes generations beyond the newest `REPO_RETAIN` (default 3), never `current` or `previous`.

A channel that fails any step keeps its published state, the others carry on, and the job fails. The `.rpm` files are signed before `SHA256SUMS` is written (`publish.sh sign dist`), so the file in the release folder is the same signed file the repository serves.

## Registration by the packages

A `.deb`, `.rpm` or Arch package installed from a file registers the repository in its post-install script, with the public key shipped **inside** the package (fetching a key at install time would be trust on first use):

| | Writes |
|---|---|
| deb | `/etc/apt/sources.list.d/agent-keepalive.sources`, `/usr/share/keyrings/agent-keepalive-archive-keyring.asc` |
| rpm | `/etc/yum.repos.d/agent-keepalive.repo` (or `/etc/zypp/repos.d/`), `/etc/pki/rpm-gpg/RPM-GPG-KEY-agent-keepalive` |
| arch | `/etc/pacman.d/agent-keepalive.conf`, an `Include` line in `/etc/pacman.conf`, the key in pacman's keyring (locally signed) |

Nothing is written if the repository is already configured (by hand or by an earlier install), if the build has no `REPO_URL`, or if the build has no repository key. Removing the package (not upgrading) removes what it wrote, and only that: each file carries an "Installed by the agent-keepalive package" marker.

### Opting out

```sh
sudo AGENT_KEEPALIVE_INSTALL_NO_REPOSITORY=1 apt install ./agent-keepalive_V_amd64.deb
# or, for good (checked on every install and upgrade):
sudo mkdir -p /etc/agent-keepalive && sudo touch /etc/agent-keepalive/no-repository
```

Then delete the entry listed above if an earlier install wrote one.

## The signing key

- Generate it once with `scripts/gen-repo-key.sh` (RSA 4096, no passphrase, five years).
- Its private half is the `REPO_SIGNING_KEY` secret (an optional passphrase goes in `REPO_SIGNING_PASSPHRASE`).
- Its public half is committed as `packaging/repository/keys/agent-keepalive.asc`; the Linux packages ship it. `publish.sh` refuses to publish when the committed key is not the signing key, because packages built from the tree would register a repository they cannot verify.
- Before it expires: `gpg --quick-set-expire <fpr> 5y`, update the secret and the committed public key, and release (both the packages and the published keyring then carry the new expiry).

## Rolling back

On the download host:

```sh
cd <DOWNLOAD_ROOT>/repo/apt     # or rpm, arch
ln -sfn "$(readlink previous)" current.new && mv -Tf current.new current
```

Clients see the previous release on their next refresh. Publishing a newer version moves them forward again.
