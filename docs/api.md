# HTTP and WebSocket API

The local server (`agent-keepalive serve`) serves the UI at `/` and the API under `/api/`. Every URL the UI uses is relative (`./api/...`), so the server works behind a reverse proxy at any path prefix.

Types are the JSON forms of `internal/model` (field names below). Times are RFC 3339 strings; a zero time is omitted. Durations are seconds (numbers). Money is USD (numbers). Errors are `{"error": "<sentence>"}` with a 4xx/5xx status.

## Authentication

Every `/api/` route except `GET /api/health` requires the install's API token:

- The server keeps a random 32-byte token (hex) in `<data dir>/token`, mode 0600, created on first start.
- The server writes it into the UI page it serves as `<meta name="ka-token" content="…">`. The UI reads the meta tag and sends it as the `X-Keepalive-Token` header on every request, and as the `token` query parameter when it opens the WebSocket.
- A missing or wrong token answers 401.

The token is what keeps other local users and other programs out: the server binds 127.0.0.1, and a cross-origin page cannot read the page that carries the token. The loopback `Host` check, the same-`Origin` check on state changes and the WebSocket upgrade, and the `Sec-Fetch-Site: cross-site` refusal still apply on top (see architecture.md, Security):

- Every `POST`, `PUT`, `PATCH` and `DELETE` must carry `Content-Type: application/json`, also when it has no body (ping, quit, update/check, `DELETE …/plan`); anything else answers 415. A cross-site page can only send such a request after a CORS preflight, and none is granted.
- `Sec-Fetch-Site: cross-site` is refused everywhere except a top-level navigation to the UI page itself (so a link to the UI works; the linking page cannot read it). State changes and the WebSocket also refuse `same-site`.
- The page is served with `Cache-Control: no-store`, because it carries the token.
- Framing: the CSP has `frame-ancestors 'self'` (plus `X-Frame-Options: SAMEORIGIN`) by default. An embedder that frames the UI from another origin passes `--frame-ancestors "<sources>"` (or `$AGENT_KEEPALIVE_FRAME_ANCESTORS`), for example `--frame-ancestors "'self' http://*.localhost:*"`; `X-Frame-Options` is then omitted.

## Routes

| Method & path | Body | Answer |
|---|---|---|
| `GET /api/health` | | `{name: "agent-keepalive", version, port}` — no token needed |
| `GET /api/info` | | `Info` (below) |
| `GET /api/sessions` | | `SessionView[]` |
| `GET /api/sessions/{key}` | | `SessionView` |
| `POST /api/sessions/{key}/plan` | `{hours}` or `{indefinite: true}` | `Plan` (201) |
| `PATCH /api/sessions/{key}/plan` | `{addHours}` or `{resume: true}` | `Plan` |
| `DELETE /api/sessions/{key}/plan` | | 204 |
| `POST /api/sessions/{key}/ping` | | `PingRecord` |
| `GET /api/sessions/{key}/pings?limit=50` | | `PingRecord[]` |
| `GET /api/ledger?from=&to=` | | `{entries: LedgerEntry[], totals: Totals}`; `from`/`to` RFC 3339, optional |
| `GET /api/missed?since=` | | `Missed`; `since` defaults to 7 days ago |
| `GET /api/settings` | | `Settings` |
| `PUT /api/settings` | `Settings` | `Settings` (validated: 0.5 ≤ maxHours ≤ 168, 1 ≤ marginMin ≤ 15, 2 ≤ scanSeconds ≤ 60) |
| `GET /api/metrics` | | `MetricsStatus` |
| `PUT /api/metrics` | `{enabled: bool}` | `MetricsStatus`; 403 when locked |
| `GET /api/metrics/preview` | | the next report exactly as it would be sent (docs/metrics.md) |
| `POST /api/metrics/reset-id` | | `MetricsStatus` with a new `installId` |
| `GET /api/update` | | `UpdateStatus` |
| `POST /api/update/check` | | `UpdateStatus` |
| `POST /api/update/apply` | | `UpdateStatus` (202); progress follows as `update` events |
| `POST /api/quit` | | 202; the server shuts down (refused with 409 when supervised) |
| `GET /api/ws?token=…` | | WebSocket upgrade, see below |

`{key}` is `<harness>:<id>`, URL-encoded (`claude%3A1b2c…`). Unknown key → 404; a plan that already exists → 409; a session with `reach: "none"` → 409 with the reason.

### `Info`

```json
{
  "name": "agent-keepalive", "version": "1.4.0", "os": "linux", "arch": "amd64",
  "profile": "public", "installKind": "appimage", "dataDir": "/home/me/.local/state/agent-keepalive",
  "supervised": false, "homepage": "https://…", "downloadUrl": "https://…"
}
```

`installKind` is one of `deb`, `rpm`, `arch`, `brew`, `appimage`, `windows-setup`, `windows-portable`, `macos-pkg`, `archive`, `dev`.

### `MetricsStatus`

```json
{
  "available": true,          // the build has a hub (profile or override); false hides the feature
  "enabled": true,
  "locked": false,            // an administrator or the profile fixed `enabled`
  "lockedBy": "",             // "admin" | "profile" when locked
  "endpoint": "https://agent-keepalive.gurupras.me/hub",
  "installId": "8d3c…",       // the pseudonymous id reports carry
  "lastSent": "2026-10-27T10:00:00Z",
  "lastError": "",
  "queued": 0                 // reports waiting to be sent
}
```

### `UpdateStatus`

```json
{
  "enabled": true,            // false: no feed in this build, --no-update, or supervised
  "current": "1.4.0",
  "latest": { "version": "1.5.0", "notes": "…markdown…", "page": "https://…", "publishedAt": "…" },
  "state": "available",       // idle | checking | available | downloading | ready | installing | error
  "progress": 0.42,           // 0..1 while downloading
  "action": "apply",          // apply: this process can update itself
                              // command: show `command` (deb/rpm/arch/brew)
                              // download: open `latest.page` (no artifact for this platform)
  "command": "sudo apt update && sudo apt install --only-upgrade agent-keepalive",
  "error": "",
  "lastCheck": "…"
}
```

`apply` downloads the artifact for this install kind, checks its sha256 against the signed feed, replaces the installed program and restarts the server into it (for `macos-pkg` it opens the downloaded package in Installer instead). The UI shows the toast whenever `state` is `available` and the user has not dismissed that version, and "Updated to vX" once it reconnects to a newer `version`.

## WebSocket `api/ws`

Text frames, one JSON `Event` (`{type, data}`) each, server → client:

| `type` | `data` |
|---|---|
| `hello` | `{version, info: Info}` — first frame |
| `sessions` | `SessionView[]` — full snapshot; sent right after `hello` and after every scan that changed anything |
| `plan` | `Plan` |
| `ping` | `PingRecord` |
| `ledger` | `LedgerEntry` |
| `settings` | `Settings` |
| `update` | `UpdateStatus` |
| `metrics` | `MetricsStatus` |

The server pings the socket every 30 s and closes it after 60 s without a pong. Clients send nothing but pongs. While disconnected the UI polls `GET /api/sessions` every 10 s and reconnects with backoff (1 s doubling to 30 s).
