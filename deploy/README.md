# Deploying agent-keepalive.gurupras.me

One host serves three things behind its own nginx:

| Path | Served by | What |
|---|---|---|
| `/` | host nginx (static) | the landing page, `deploy/site/` |
| `/download/` | host nginx (static) | release files, `latest.json`, the apt/rpm/arch repositories (`/download/repo/`) |
| `/hub/` | `hub` container, proxied | the usage-report hub; its dashboard asks for `HUB_DASHBOARD_TOKEN` |

The hub stores reports in the `db` container (Postgres) and listens on `127.0.0.1:${HUB_PORT}` only. The `upload` container is the release workflow's login: an sshd that accepts one key and sees nothing of the host but the download directory. All three come from `docker-compose.yml`; nothing on this host builds a release.

## Requirements

- A Linux host with Docker and the compose plugin, and nginx.
- A TLS certificate for the host name (a wildcard one works).
- A DNS **A record** `agent-keepalive.gurupras.me` → the host's public IPv4 (and AAAA if it has IPv6).
- `UPLOAD_PORT` (2222) reachable from the internet, if the host has a firewall.

## Layout on the host

```
~/workspace/agent-keepalive-manager/     a clone of this repository
  deploy/.env                            settings and secrets (not in git)
  deploy/upload/authorized_keys          the release key's public half (not in git)
<data disk>/agent-keepalive/download/    DOWNLOAD_ROOT: release files, update feed, repositories
```

## First deploy

```sh
git clone https://github.com/gurupras/agent-keepalive-manager.git ~/workspace/agent-keepalive-manager
cd ~/workspace/agent-keepalive-manager/deploy
cp .env.example .env     # DOWNLOAD_ROOT, UPLOAD_UID (`id -u`), HUB_DB_PASSWORD, HUB_DASHBOARD_TOKEN
mkdir -p "$(grep ^DOWNLOAD_ROOT= .env | cut -d= -f2)"

# The release key (make it on your own machine; the private half becomes the
# DOWNLOAD_SSH_KEY secret). `restrict` turns off forwarding and terminals.
echo "restrict $(cat release-key.pub)" > upload/authorized_keys

docker compose up -d --build
docker compose logs upload | grep "host key"                 # the fingerprint CI will pin
curl -fsS http://127.0.0.1:6720/hub/healthz                  # ok

# nginx: fill in nginx/agent-keepalive.conf (SITE_DIR = this deploy/site,
# WWW_ROOT = the directory holding download/, the certificate), link it and reload.
sudo ln -s "$PWD/nginx/agent-keepalive.conf" /etc/nginx/sites-enabled/agent-keepalive.gurupras.me.conf
sudo nginx -t && sudo systemctl reload nginx
curl -fsS https://agent-keepalive.gurupras.me/hub/healthz    # ok
curl -fsSI https://agent-keepalive.gurupras.me/download/     # 200
```

nginx must be able to read `deploy/site` and the download directory (every directory on the way needs `o+x`).

### The release workflow's login

```sh
ssh-keygen -t ed25519 -N "" -C agent-keepalive-release -f release-key         # on your machine
ssh-keyscan -p 2222 -t ed25519 agent-keepalive.gurupras.me > known_hosts       # compare with the logged fingerprint
```

Secrets: `DOWNLOAD_SSH_KEY` (contents of `release-key`), `DOWNLOAD_SSH_KNOWN_HOSTS` (contents of `known_hosts`), `DOWNLOAD_SSH_HOST` (`agent-keepalive.gurupras.me`), `DOWNLOAD_SSH_PORT` (`2222`), `DOWNLOAD_SSH_USER` (`upload`) and `DOWNLOAD_ROOT` (`/download`, the path inside the container). See [docs/releasing.md](../docs/releasing.md).

The container runs with a read-only root filesystem, every capability dropped except the few sshd needs to switch users, and no Docker socket. A leaked release key can change the download directory and nothing else; the update feed and the repositories stay signed by keys that never reach this host. To revoke the key, empty `upload/authorized_keys` and `docker compose restart upload`. The host key lives in the `upload-keys` volume, so `known_hosts` stays valid across restarts.

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

- **Landing page, nginx config or compose file**: `git pull`; `docker compose up -d --build` for container changes, then `sudo nginx -t && sudo systemctl reload nginx` after a config change.
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
