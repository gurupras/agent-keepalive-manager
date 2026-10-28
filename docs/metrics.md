# Usage metrics

agent-keepalive reports how much keeping caches warm saves to a **hub** (`agent-keepalive-hub`), so the people running one can see the totals across every install. This page lists everything a report contains. Nothing else is sent.

## On, off, and who decides

- Reporting is **on by default** in the public build. Turn it off in Settings › Usage reports, with `agent-keepalive metrics off`, or with `AGENT_KEEPALIVE_METRICS=off`. Off takes effect at once: nothing more is sent, and reports waiting in the queue are deleted.
- Where reports go comes from the build's distribution profile (`METRICS_URL`). `AGENT_KEEPALIVE_METRICS_URL` or the admin config's `metrics.url` overrides it. A build whose profile has no hub, and that has no override, never sends anything and hides the setting.
- An administrator can fix the choice for every user of a machine in the admin config file (see architecture.md, Distribution profiles), for example `{"metrics": {"enabled": true, "url": "https://keepalive-hub.example.internal"}, "locked": ["metrics.enabled", "metrics.url"]}`. A profile with `METRICS_LOCKED=true` does the same for a whole build. A locked setting is shown as locked, with who locked it.
- `agent-keepalive metrics preview` (and Settings › Usage reports › Preview) prints the next report exactly as it would be sent.

## What a report contains

One report per install per UTC day, sent once that day is over (and at start-up for any day not sent yet, up to 30 days back). Reports that cannot be sent wait in `<data dir>/metrics-queue/` and are retried with backoff.

```json
{
  "schema": 1,
  "installId": "8d3c6f0e-2b7a-4f5e-9c1d-3a4b5c6d7e8f",
  "day": "2026-10-26",
  "version": "1.4.0",
  "os": "linux",
  "arch": "amd64",
  "profile": "public",
  "installKind": "deb",
  "sessions": { "claude": 4, "codex": 1 },
  "plans": { "started": 3, "indefinite": 1, "hours": 21.5 },
  "pings": { "sent": 17, "verified": 16, "cold": 1, "failed": 0, "costUsd": 0.84 },
  "outcomes": [
    { "harness": "claude", "model": "claude-opus-4-1", "outcome": "saved", "endedBy": "returned",
      "count": 2, "pings": 9, "pingCostUsd": 0.41, "avoidedUsd": 7.80, "netUsd": 7.39,
      "contextTokens": 310000, "idleMinutes": 410 }
  ]
}
```

| Field | Meaning |
|---|---|
| `installId` | A random UUID made on this machine the first time a report is built, kept in `<data dir>/install-id`. It lets the hub count installs and de-duplicate a day sent twice. It is not derived from anything about you or the machine. `agent-keepalive metrics reset-id` replaces it. |
| `day` | The UTC day the counts cover. No other time is sent. |
| `version`, `os`, `arch`, `profile`, `installKind` | The build and how it was installed. |
| `sessions` | How many distinct sessions of each harness were seen running that day. |
| `plans` | Plans started that day, how many were indefinite, and the hours requested in total. |
| `pings` | Keepalive pings sent that day, by result, and what they cost. |
| `outcomes` | Ledger entries closed that day, summed per harness, model, outcome and reason. `contextTokens` and `idleMinutes` are sums. |

**Never sent:** names, user names, e-mail, host names, IP addresses chosen by us, paths, project names, session titles or ids, prompts, transcript content, models' answers, settings other than the above. The hub does not store the sender's IP address.

## The hub

```sh
docker run -d -p 8080:8080 -v hub-data:/data ghcr.io/gurupras/agent-keepalive-hub
# or
agent-keepalive-hub --listen :8080 --data-dir /var/lib/agent-keepalive-hub
```

| Flag / env | Default | |
|---|---|---|
| `--listen` / `HUB_LISTEN` | `:8080` | |
| `--data-dir` / `HUB_DATA_DIR` | `./data` | SQLite database lives here |
| `--database-url` / `HUB_DATABASE_URL` | | `postgres://…` to use Postgres instead of SQLite |
| `--base-path` / `HUB_BASE_PATH` | `/` | serve under a path, e.g. `/hub` |
| `--ingest-token` / `HUB_INGEST_TOKEN` | | require `Authorization: Bearer <token>` on reports (clients: `metrics.token` in the admin config, or `AGENT_KEEPALIVE_METRICS_TOKEN`) |
| `--dashboard` / `HUB_DASHBOARD` | `public` | `public`, `token` (needs `HUB_DASHBOARD_TOKEN`; the dashboard asks for it) or `off` |
| `--trust-proxy` / `HUB_TRUST_PROXY` | `false` | use `X-Forwarded-For` for rate limiting (never stored) |
| `--retention-days` / `HUB_RETENTION_DAYS` | `730` | reports older than this are deleted |

### Hub API

| Method & path | |
|---|---|
| `POST {base}/v1/reports` | one report (body above, ≤ 64 KiB). 202 stored, 200 duplicate (same `installId` + `day`: the newer one replaces it), 400 invalid, 401 bad token, 429 rate limited (60 reports per sender per hour). |
| `GET {base}/v1/summary?from=&to=` | `{installs, activeInstalls, saved, wasted, net, pingCost, pings, cold, returns}` over the days `from`..`to` (YYYY-MM-DD) |
| `GET {base}/v1/series?from=&to=&metric=net|saved|wasted|installs|pings` | `[{day, value}]` |
| `GET {base}/v1/breakdown?from=&to=&by=model|harness|version|os|installKind|profile` | `[{key, installs, saved, wasted, net, pings}]` |
| `GET {base}/healthz` | `ok` |
| `GET {base}/` | the dashboard |

A report is validated against this schema: unknown fields are refused, strings are limited to 64 characters and must match `[A-Za-z0-9._:/-]`, counts and amounts must be finite and non-negative (`netUsd` may be negative), and `day` must not be in the future or more than 30 days old.
