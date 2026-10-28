# Deploying agent-keepalive.gurupras.me

One host serves three things behind an existing Traefik:

| Path | Container | What |
|---|---|---|
| `/` | `downloads` (nginx) | the landing page, `deploy/site/` |
| `/download/` | `downloads` (nginx) | release files, `latest.json`, the apt/rpm/arch repositories (`/download/repo/`) |
| `/hub/` | `hub` (`ghcr.io/gurupras/agent-keepalive-hub`) | the usage-report hub; its dashboard asks for `HUB_DASHBOARD_TOKEN` |

The release workflow writes into the download directory over ssh; nothing on this host builds anything.

## Requirements

- A Linux host with Docker and the compose plugin.
- Traefik already running there, watching Docker labels, with an HTTPS entrypoint and a certificate resolver (Let's Encrypt). Their names go in `.env`.
- A DNS **A record** `agent-keepalive.gurupras.me` → the host's public IPv4 (and an AAAA record if it has IPv6). Create it before the first start, so the certificate resolver can answer its challenge.

## First deploy

```sh
# 1. The code (only deploy/ is used)
git clone https://github.com/gurupras/agent-keepalive-manager.git ~/agent-keepalive
cd ~/agent-keepalive/deploy
cp .env.example .env            # set TRAEFIK_*, DOWNLOAD_ROOT, HUB_*

# 2. The download directory, owned by the user the release workflow logs in as
sudo mkdir -p /srv/agent-keepalive/download
sudo chown deploy: /srv/agent-keepalive/download

# 3. Start
docker compose pull
docker compose up -d
curl -fsS https://agent-keepalive.gurupras.me/hub/healthz    # ok
curl -fsSI https://agent-keepalive.gurupras.me/download/     # 200
```

### The release workflow's login

The workflow uploads with rsync over ssh as a dedicated user (`deploy` above):

```sh
sudo adduser --disabled-password --gecos "" deploy
ssh-keygen -t ed25519 -N "" -C agent-keepalive-release -f release-key   # on your machine
# append release-key.pub to ~deploy/.ssh/authorized_keys on the host
ssh-keyscan -t ed25519 agent-keepalive.gurupras.me > known_hosts         # check the fingerprint
```

Then set the repository secrets `DOWNLOAD_SSH_KEY` (contents of `release-key`), `DOWNLOAD_SSH_KNOWN_HOSTS` (contents of `known_hosts`), `DOWNLOAD_SSH_HOST`, `DOWNLOAD_SSH_USER` (`deploy`) and `DOWNLOAD_ROOT` (`/srv/agent-keepalive/download`). The host needs `rsync` installed. See [docs/releasing.md](../docs/releasing.md).

## Layout of the download directory

```
<DOWNLOAD_ROOT>/
  latest.json                  the update feed; replaced last, atomically, by each stable release
  1.2.0/                       one folder per version: every artifact + SHA256SUMS + latest.json
  repo/
    agent-keepalive.sources    setup files for users adding the repository by hand
    agent-keepalive.list
    agent-keepalive.repo
    pacman.conf
    agent-keepalive-archive-keyring.{asc,gpg}
    FINGERPRINT
    apt/  rpm/  arch/
      generations/<UTC stamp>-<version>/
      current  -> generations/…   what clients read
      previous -> generations/…   one step back
```

## Updating

- **Landing page or nginx config**: `git pull && docker compose up -d downloads` (nginx reads its config at start; `docker compose restart downloads` after a config-only change).
- **Hub**: each release pushes `ghcr.io/gurupras/agent-keepalive-hub:<version>` and, for stable releases, `:latest`. `docker compose pull hub && docker compose up -d hub`. Pin a version with `HUB_TAG` in `.env`.
- **Releases**: nothing to do here; the workflow uploads them.

## Rolling back a repository

Point `current` back at `previous`:

```sh
cd /srv/agent-keepalive/download/repo/apt
ln -sfn "$(readlink previous)" current.new && mv -Tf current.new current
```

To withdraw a release from updaters, put the previous version's `latest.json` back: `cp 1.1.0/latest.json latest.json.new && mv latest.json.new latest.json`.

## Backups

The hub's database is in the `hub-data` volume (`docker run --rm -v agent-keepalive_hub-data:/d -v "$PWD":/b alpine tar czf /b/hub-data.tgz -C /d .`). The download directory can always be rebuilt from the GitHub releases.
