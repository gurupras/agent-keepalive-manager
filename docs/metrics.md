# Usage metrics

agent-keepalive reports how much keeping caches warm saves to a **hub** (`agent-keepalive-hub`), so the people running one can see the totals across every install. This page lists everything a report contains. Nothing else is sent.

## On, off, and who decides

- Reporting is **on by default** in the public build. Turn it off in Settings › Usage reports, with `agent-keepalive metrics off`, or with `AGENT_KEEPALIVE_METRICS=off`. Off takes effect at once: nothing more is sent, and reports waiting in the queue are deleted.
- Where reports go comes from the build's distribution profile (`METRICS_URL`). `AGENT_KEEPALIVE_METRICS_URL` or the admin config's `metrics.url` overrides it. A build whose profile has no hub, and that has no override, never sends anything and hides the setting.
- An administrator can fix the choice for every user of a machine in the admin config file (see architecture.md, Distribution profiles), for example `{"metrics": {"enabled": true, "url": "https://keepalive-hub.example.internal"}, "locked": ["metrics.enabled", "metrics.url"]}`. A profile with `METRICS_LOCKED=true` does the same for a whole build. A locked setting is shown as locked, with who locked it.
- `agent-keepalive metrics preview` (and Settings › Usage reports › Preview) prints the next report exactly as it would be sent.

## What a report contains

One report per install per UTC day, sent once that day is over (and at start-up for any day not sent yet, up to 30 days back). Reports that cannot be sent wait in `<data dir>/metrics-queue/` and are retried with backoff.

A new install, and each new version, also sends the report of the current day so far, 10 minutes after it first starts. The hub then counts it within minutes of an install or update, instead of the next day. The day's full report replaces this early one at the hub, which keeps one report per install and day. Turning reporting off in those 10 minutes means nothing is sent. The app's first window says that usage reports are on and links to the setting.

```json
{
  "schema": 2,
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
  ],
  "experiments": [
    { "experiment": "busy-ping-subagents-1", "arm": "ping", "harness": "claude", "model": "claude-opus-4-1",
      "episodes": 3, "returned": 3, "warm": 2, "pings": 5, "pingCalls": 5,
      "pingCostUsd": 0.35, "rewriteUsd": 1.20, "costUsd": 1.55, "costSqUsd": 1.61,
      "contextTokens": 540000, "quietMinutes": 96 }
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
| `experiments` | Schema 2. Experiment episodes closed that day, summed per experiment, arm, harness and model. See below. |

`schema` is 2 for a report with `experiments`. A report without any (the install takes part in no experiment, or none of its episodes closed that day) leaves the field out and is schema 1, as from a client that predates experiments; the two are otherwise the same.

### Experiments

An experiment compares two ways of handling a session the harness reports as busy while its main agent makes no API calls of its own (for example, while background sub-agents run): `hold` (no pings, the behaviour without experiments) and `ping` (keep pinging). An **episode** is one such quiet stretch of a session with a keepalive plan, and each episode is assigned to an arm at random; [experiments.md](experiments.md) describes how, and the guards that apply. It ends when the main agent makes its next API call (`returned`), when the session goes idle without one, or when the plan or session ends first. Its cost is the pings sent during it plus the cache writes of the call that ended it; for an episode that ended without such a call, the cost of re-writing its context if the cache had expired by then, else nothing.

| Field | Meaning |
|---|---|
| `experiment` | The experiment's id, e.g. `busy-ping-subagents-1`. |
| `arm` | `hold` or `ping`. |
| `harness`, `model` | The session's harness and model (`model` may be empty). |
| `episodes` | Episodes closed that day. |
| `returned` | Of those, the ones ended by the main agent's next API call. |
| `warm` | Of `returned`, the ones whose call read the cache. |
| `pings`, `pingCalls` | Pings sent during the episodes, and the API calls their replies took. Always 0 in `hold`. |
| `pingCostUsd` | What those pings cost. |
| `rewriteUsd` | The cache writes of the calls that ended the episodes (they may write more than `contextTokens`, if the context grew meanwhile). |
| `costUsd` | `pingCostUsd` + `rewriteUsd`. |
| `costSqUsd` | The sum, over the episodes, of each one's cost squared, so the hub can compute the spread of costs. |
| `contextTokens`, `quietMinutes` | Sums: each episode's context size, and how long it lasted. |

**Never sent:** names, user names, e-mail, host names, IP addresses chosen by us, paths, project names, session titles or ids, prompts, transcript content, models' answers, settings other than the above. Experiments add only experiment ids, arm names, harness and model names, counts and sums. The hub does not store the sender's IP address. To limit new installs per network (below) it keeps, for the current UTC day only, an HMAC of the sender's /24 (IPv4) or /64 (IPv6) under a random salt that is replaced, and the old one deleted, every day; it cannot be turned back into an address or matched across days.

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
| `--max-daily-usd` / `HUB_MAX_DAILY_USD` | `2000` | refuse (400) a report whose `avoidedUsd` adds up to more than this; `0` is no cap |
| `--new-installs-per-network` / `HUB_NEW_INSTALLS_PER_NETWORK` | `5` | installs never seen before that one /24 or /64 may add per UTC day; more get 429 until the next UTC midnight, and their client retries then; `0` is no limit |
| `--established-days` / `HUB_ESTABLISHED_DAYS` | `3` | the dashboard counts an install once it has sent this many daily reports (`installs=all` counts every one); `0` or `1` counts all |

### Hub API

| Method & path | |
|---|---|
| `POST {base}/v1/reports` | one report (body above, ≤ 64 KiB). 202 stored, 200 duplicate (same `installId` + `day`: the newer one replaces it), 400 invalid, 401 bad token, 429 rate limited (60 reports per sender per hour, or the network's new installs for the day are used up), with `Retry-After`. |
| `GET {base}/v1/summary?from=&to=&installs=` | `{installs, activeInstalls, saved, wasted, net, pingCost, pings, cold, returns, pending, medianNet, trimmedNet, trimmed, topShare, establishedDays}` over the days `from`..`to` (YYYY-MM-DD). `pending` is the installs not counted yet, `medianNet` the median install's net, `trimmedNet` the net without the `trimmed` installs (1%, rounded down) furthest from zero, `topShare` the largest install's share of the summed \|net\| |
| `GET {base}/v1/series?from=&to=&installs=&metric=net|saved|wasted|installs|pings` | `[{day, value}]` |
| `GET {base}/v1/breakdown?from=&to=&installs=&by=model|harness|version|os|installKind|profile` | `[{key, installs, saved, wasted, net, pings}]` |
| `GET {base}/v1/experiments?from=&to=&installs=` | `{experiments: [{experiment, arms: [{arm, installs, episodes, returned, warm, pings, pingCalls, meanCostUsd, seCostUsd, warmRate, pingsPerEpisode, meanQuietMinutes}], diff: {meanCostUsd, ciLowUsd, ciHighUsd, significant} \| null}]}`, by experiment id, arms `hold` then `ping`. `meanCostUsd` is the mean cost per episode and `seCostUsd` its standard error; `warmRate` is `warm / returned`. `diff` is `ping` minus `hold` with a 95% interval (Welch's, with Student's t at the Welch–Satterthwaite degrees of freedom), `significant` when it excludes 0, and null while an arm has fewer than 10 episodes or none. Episodes are counted as independent, though those of one install are alike, so the interval is too narrow when a few installs supply most episodes |
| `GET {base}/v1/session` | `{mode: "public"|"token", authenticated}`; needs no login (404 with `--dashboard off`) |
| `POST {base}/v1/login` | `{"token": "…"}` → 204 with the session cookie, 401 wrong token (token mode only, else 404; 60 attempts per sender per hour) |
| `POST {base}/v1/logout` | 204, clears the cookie |
| `GET {base}/healthz` | `ok`, or 503 when the database is unreachable |
| `GET {base}/` | the dashboard |

The hub accepts schemas 1 and 2: `experiments` is required in schema 2 and refused in schema 1. A hub that predates schema 2 refuses those reports, so upgrade the hub before the clients that report to it.

A report is validated against this schema: unknown fields are refused, strings are limited to 64 characters and must match `[A-Za-z0-9._:/-]`, counts and amounts must be finite and non-negative (`netUsd` may be negative), and `day` must not be in the future or more than 30 days old. `installs` is `established` (the default: only installs with `--established-days` reports) or `all`.

The numbers must also be possible for one install in one day, or the report is refused with 400 (and the client drops it): `version` is a release (`1.2.3`, `1.2.3-rc.1`) or `dev`; `plans.indefinite` ≤ `plans.started` and `plans.hours` ≤ 168 per plan; `pings.verified`, `pings.cold` and `pings.verified + pings.failed` ≤ `pings.sent`; `pings.sent` ≤ 1440 per session seen (one a minute); at most $25 per ping. Each outcome has `count` ≥ 1, at most 10M `contextTokens` per entry, an `avoidedUsd` no larger than writing its context at $100 per 1M tokens, a `pingCostUsd` of at most $25 per ping, and `netUsd` = `avoidedUsd` − `pingCostUsd` (to within $0.01). There are at most 1000 experiment entries, no two with the same `experiment`, `arm`, `harness` and `model`, and at most 100 `episodes` in all (twice the 50 a day an install opens). Each has `episodes` ≥ 1, `warm` ≤ `returned` ≤ `episodes`, no pings or ping cost in `hold`, at most 50 `pingCalls` per ping, at most 10M `contextTokens` and 168 hours of `quietMinutes` per episode, a `pingCostUsd` of at most $25 per ping, a `rewriteUsd` no larger than writing 10M tokens per episode at $100 per 1M tokens, `costUsd` = `pingCostUsd` + `rewriteUsd` (to within $0.01), and `costUsd`² / `episodes` ≤ `costSqUsd` ≤ `costUsd`² (to within $0.01 plus one millionth of `costUsd`²).
