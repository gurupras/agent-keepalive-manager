# Deploying agent-keepalive.gurupras.me

One host serves three things behind its own nginx:

| Path | Served by | What |
|---|---|---|
| `/` | host nginx (static) | the landing page, `deploy/site/` |
| `/download/` | host nginx (static) | release files, `latest.json`, the apt/rpm/arch repositories (`/download/repo/`) |
| `/hub/` | `hub` container, proxied | the usage-report hub; its dashboard asks for `HUB_DASHBOARD_TOKEN` |

The hub stores reports in the `db` container (Postgres). Both containers come from `docker-compose.yml`; the hub listens on `127.0.0.1:${HUB_PORT}` only. The release workflow writes into the download directory over ssh; nothing on this host builds anything.

## Requirements

- A Linux host with Docker and the compose plugin, nginx, and `rsync`.
- A TLS certificate for the host name (a wildcard one works).
- A DNS **A record** `agent-keepalive.gurupras.me` → the host's public IPv4 (and AAAA if it has IPv6).

## Layout on the host

```
~/workspace/agent-keepalive-manager/
  deploy/               this directory (docker-compose.yml, .env, nginx/, site/)
  www/download/         DOWNLOAD_ROOT: what the release workflow uploads
```

## First deploy

```sh
cd ~/workspace/agent-keepalive-manager/deploy
cp .env.example .env            # set HUB_DB_PASSWORD and HUB_DASHBOARD_TOKEN
mkdir -p ../www/download

docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:6720/hub/healthz                  # ok

# nginx: fill in nginx/agent-keepalive.conf (SITE_DIR, WWW_ROOT, certificate),
# link it into sites-enabled and reload.
sudo ln -s "$PWD/nginx/agent-keepalive.conf" /etc/nginx/sites-enabled/agent-keepalive.gurupras.me.conf
sudo nginx -t && sudo systemctl reload nginx
curl -fsS https://agent-keepalive.gurupras.me/hub/healthz    # ok
curl -fsSI https://agent-keepalive.gurupras.me/download/     # 200
```

nginx must be able to read `deploy/site` and `www/download` (every directory on the way needs `o+x`).

### The release workflow's login

The workflow uploads with rsync over ssh and runs a few commands there (moving `latest.json` and the repository `current` links into place). Give it its own key:

```sh
ssh-keygen -t ed25519 -N "" -C agent-keepalive-release -f release-key   # on your machine
# append release-key.pub to ~/.ssh/authorized_keys of the upload user on the host
ssh-keyscan -t ed25519 agent-keepalive.gurupras.me > known_hosts         # check the fingerprint
```

Then set the repository secrets `DOWNLOAD_SSH_KEY` (contents of `release-key`), `DOWNLOAD_SSH_KNOWN_HOSTS` (contents of `known_hosts`), `DOWNLOAD_SSH_HOST`, `DOWNLOAD_SSH_USER` and `DOWNLOAD_ROOT` (the absolute path of `www/download`). See [docs/releasing.md](../docs/releasing.md).

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

- **Landing page or nginx config**: update the files, then `sudo nginx -t && sudo systemctl reload nginx` after a config change.
- **Hub**: each release pushes `ghcr.io/gurupras/agent-keepalive-hub:<version>` and, for stable releases, `:latest`. `docker compose pull hub && docker compose up -d hub`. Pin a version with `HUB_TAG` in `.env`.
- **Releases**: nothing to do here; the workflow uploads them.

## Rolling back a repository

Point `current` back at `previous`:

```sh
cd <DOWNLOAD_ROOT>/repo/apt
ln -sfn "$(readlink previous)" current.new && mv -Tf current.new current
```

To withdraw a release from updaters, put the previous version's `latest.json` back: `cp 1.1.0/latest.json latest.json.new && mv latest.json.new latest.json`.

## Backups

The reports are in Postgres:

```sh
docker compose exec -T db pg_dump -U hub hub | gzip > hub-$(date +%F).sql.gz
gunzip -c hub-2026-10-28.sql.gz | docker compose exec -T db psql -U hub hub   # restore into an empty database
```

The download directory can always be rebuilt from the GitHub releases.
