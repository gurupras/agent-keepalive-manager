# Self-hosting: your own hub, downloads and builds

Everything the public distribution uses can be run by anyone: the hub that collects usage reports, the download host with the update feed and package repositories, and builds that point at them. An organisation typically does all three, so its installs report only to its own hub and update only from its own host.

## Running a hub

```sh
docker run -d --name agent-keepalive-hub -p 8080:8080 -v hub-data:/data \
  ghcr.io/gurupras/agent-keepalive-hub:latest
```

or the `agent-keepalive-hub_V_linux_<arch>.tar.gz` binary: `agent-keepalive-hub --listen :8080 --data-dir /var/lib/agent-keepalive-hub`. Configuration (base path, Postgres, ingest and dashboard tokens, retention) is in [metrics.md](metrics.md#the-hub). Behind a reverse proxy at a path, set `HUB_BASE_PATH` (e.g. `/hub`) and `HUB_TRUST_PROXY=true`.

Pointing existing installs at it, without rebuilding:

- per user: `AGENT_KEEPALIVE_METRICS_URL=https://hub.example.internal`
- per machine (administrators), in the admin config file (`/etc/agent-keepalive/config.json`, `%ProgramData%\agent-keepalive\config.json`, `/Library/Application Support/agent-keepalive/config.json`):
  ```json
  { "metrics": { "enabled": true, "url": "https://hub.example.internal", "token": "…" },
    "locked": ["metrics.enabled", "metrics.url"] }
  ```

## Running a download host

[`deploy/`](../deploy/README.md) is the public host's setup: Traefik in front, an nginx container serving `/download/` (autoindex, the right MIME types, no caching of `latest.json` and repository indexes) and a landing page, and the hub at `/hub`. Copy it, change `SITE_HOST` in `.env`, and replace `deploy/site/`.

The release scripts need nothing on the host but ssh, rsync and a directory (`DOWNLOAD_ROOT`) that nginx serves at the profile's `DOWNLOAD_URL`, with the repositories under `<DOWNLOAD_ROOT>/repo` served at `REPO_URL` (`REPO_REMOTE_DIR` changes the subdirectory).

## Building for your organisation

A **profile** decides where a build reports and updates from. Copy `profiles/public.env` somewhere outside the repository:

```sh
PROFILE_NAME=example
VENDOR=Example Corp
HOMEPAGE=https://keepalive.example.internal
METRICS_URL=https://keepalive.example.internal/hub
METRICS_DEFAULT=on
METRICS_LOCKED=true            # users cannot turn reporting off; the admin file still can
UPDATE_FEED_URL=https://keepalive.example.internal/download/latest.json
UPDATE_PUBLIC_KEY=<hex public key from: go run ./tools/release keygen>
DOWNLOAD_URL=https://keepalive.example.internal/download
REPO_URL=https://keepalive.example.internal/download/repo
```

Leave `METRICS_URL` empty for builds that never report, and `UPDATE_FEED_URL` empty for builds that never update themselves. Then:

```sh
scripts/gen-repo-key.sh /secure/place          # your repository key
export PROFILE=/path/to/example.env REPO_PUBLIC_KEY=/secure/place/agent-keepalive.asc
make ui && make dist                           # on Linux; make macos-pkg on a Mac
```

The Linux packages register *your* repository with *your* key, the binaries report to *your* hub and trust only feeds signed with *your* update key.

### In CI

- **GitHub Actions** (a fork or a private mirror of this repository): put the profile's content in the `ORG_PROFILE` secret, or commit it and set the `PROFILE_FILE` variable, and set the secrets in [releasing.md](releasing.md#secrets-and-variables) for your own keys and host. The release workflow runs unchanged. Set `REPO_PUBLIC_KEY` in the workflow environment, or replace `packaging/repository/keys/agent-keepalive.asc` in your fork, so the packages ship your repository key.
- **Any other CI**: the workflow is a sequence of scripts (see [releasing.md](releasing.md#doing-it-by-hand)); run them with your profile and environment. Linux runners need Go, Node, nfpm, makensis (or Docker) and Docker for the repositories; the macOS package needs a Mac.

### Homebrew

`packaging/homebrew/render-formula.sh` renders a formula for any base URL (`HOMEPAGE` sets its homepage) and `publish-tap.sh` pushes it to `HOMEBREW_TAP_REPO`. The formula installs the archive binary and marks it as a brew install, so it points users at `brew upgrade`.
