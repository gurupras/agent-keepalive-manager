# Architecture

agent-keepalive is two programs built from one Go module, and a desktop window for the first:

| Program | What it is |
|---|---|
| `agent-keepalive` | A local server with a web UI. It discovers the Claude Code and Codex CLI sessions running on this machine and, for the sessions you choose, keeps their provider prompt cache warm. It keeps a ledger of what that cost and saved, and (unless turned off) reports usage to a hub. |
| `agent-keepalive-hub` | The central server that receives usage reports from many installs and shows them on a dashboard. Anyone can run one; an organisation runs its own. |
| `agent-keepalive-desktop` | The UI of `agent-keepalive` in a native window (Tauri, in Rust). It holds no state and talks only to the server's page; `agent-keepalive app` starts it. The packages, AppImage, Windows and macOS installers carry it; the archives and Homebrew do not. |

Both Go programs are pure Go (`CGO_ENABLED=0`) and embed their Vue UI, so each is a single file.

## Repository layout

```
cmd/agent-keepalive/         the local server + CLI (serve, app, open, mcp, status, stop, update, metrics, version)
cmd/agent-keepalive-hub/     the hub
internal/
  model/                     shared types: Session, Activity, Plan, PingRecord, LedgerEntry, Settings, Event
  proc/                      process inspection, one file per OS (linux /proc, darwin sysctl, windows toolhelp)
  claude/                    Claude Code discovery (session registry, transcripts) and delivery (messaging socket)
  codex/                     Codex CLI discovery (processes, rollouts) and delivery (`codex queue`)
  pricing/                   list-price table + pricing.json override, break-even math
  store/                     SQLite (modernc.org/sqlite): plans, pings, ledger, settings, kv
  scheduler/                 the core: scan loop, plans, pings, verification, ledger segments, missed what-if
  api/                       HTTP API + WebSocket events + SPA serving + loopback guard
  app/                       wiring: Serve(ctx, Config) used by the CLI
  mcp/                       the MCP server (`agent-keepalive mcp`): tools over the local API
  instance/                  data dir, port selection, single-instance lock, port file, find running
  distro/                    the link-time distribution profile (URLs, metrics default)
  config/                    runtime config: user file, admin file (locks), env overrides
  telemetry/                 install id, report building, batching, sending to the hub
  update/                    signed feed, install-kind detection, download/verify/swap/restart
  hub/                       the hub: ingest, storage, aggregation queries, dashboard API
web/                         the local UI (Vue 3, Vite, Tailwind 4, TypeScript), embedded by web/embed.go
hub/web/                     the hub dashboard (same stack), embedded by hub/web/embed.go
desktop/                     the desktop window (Tauri 2, Rust): the server's page in a native webview
profiles/                    distribution profiles (public.env)
packaging/                   nfpm (deb/rpm/arch), AppImage, NSIS (setup .exe), portable .exe, macOS .pkg, repositories
deploy/                      docker compose for the hub + downloads host
.github/workflows/           ci.yml (every push/PR), release.yml (v* tags)
```

## Discovery, delivery, scheduling

Read-only discovery, never keystrokes:

| | Claude Code | Codex CLI |
|---|---|---|
| Config dir | `$CLAUDE_CONFIG_DIR`, default `~/.claude` | `$CODEX_HOME`, default `~/.codex` |
| Live sessions | `sessions/<pid>.json`; the pid's start time (from `internal/proc`) must match the record, so a reused pid is never mistaken for the session | processes named `codex`, and the rollout each holds open (Linux: `/proc/<pid>/fd`; elsewhere the newest rollout whose `session_meta` cwd and start match the process) |
| Transcript | `projects/*/<sessionId>.jsonl` | `sessions/**/rollout-*-<thread>.jsonl` |
| Usage | `message.usage` per API call, deduplicated by message id | `token_count` per API call |
| Delivery | one JSON line to the session's messaging socket (`messagingSocketPath`) | `codex queue --thread <id> --message <text>` with the session's own binary |

Every ping carries a `[keepalive id=…]` marker, so scanners tell pings from real prompts. A plan pings at `TTL − margin` after the last API call of any kind, never while the session is working, and verifies each ping against the transcript (a reply that re-wrote the whole context is `cold`).

Ledger semantics: an idle segment starts at the last real activity and ends when the user returns (saved = context × (write − read) − ping cost, only if the gap exceeded the TTL), when the plan ends or the session exits (wasted = ping cost), or when a ping finds the cache cold.

## Live updates: WebSocket, not SSE

The UI gets events over a WebSocket at `api/ws` (snapshot on connect, then `sessions`, `plan`, `ping`, `ledger`, `settings`, `update`, `metrics`). Some reverse proxies buffer whole responses, which stalls a server-sent event stream; WebSocket upgrades are relayed as a byte stream. While the socket is down the UI polls every 10 s and reconnects with backoff.

## Embedding under a supervisor

The UI uses relative URLs and hash routing, so it works at any path prefix, including inside an iframe behind a proxy. For a host app that runs agent-keepalive as a supervised process:

- `PORT` (or `--port`) is taken exactly; `--data-dir` / `$AGENT_KEEPALIVE_DATA_DIR` sets state.
- `--exit-on-stdin-eof` ends the server when the supervisor goes away.
- `--no-update` turns self-update off (the supervisor owns the binary).
- `--ui-addon <dir>` serves `<dir>` under `addon/` and links its `addon.css` and `addon.js` into the page, so the host can restyle the UI: the palette is the `--ka-*` CSS variables, and `<html data-theme="light|dark">` picks the scheme.
- Two schedulers never run against the same sessions: a second instance (any data dir) that finds the standalone one running proxies to it.

## Security

- Binds `127.0.0.1` only. Requests whose `Host` is not a loopback name on our port are refused (DNS rebinding).
- State-changing requests need `Content-Type: application/json` and, when `Origin` is present, a same-origin one; `Sec-Fetch-Site: cross-site` is always refused. The WebSocket upgrade applies the same Origin check.
- Never reads credential files; reads only the environment variables it needs from other processes; never logs values.

## Distribution profiles

`internal/distro` holds the URLs a build talks to, set at link time from a profile file:

| Key | Meaning |
|---|---|
| `METRICS_URL` | the hub; empty disables reporting |
| `METRICS_DEFAULT` | `on`/`off` before the user chooses |
| `METRICS_LOCKED` | users cannot change reporting (admin config still can) |
| `UPDATE_FEED_URL`, `UPDATE_PUBLIC_KEY` | signed release feed; empty disables self-update |
| `DOWNLOAD_URL`, `REPO_URL` | download page; apt/rpm/arch repository base |

`profiles/public.env` is the public build. An organisation keeps its own profile in its own CI and runs `PROFILE=org.env make release`; nothing about it appears in this repository. A plain `go build` has no profile and contacts nothing.

Runtime overrides, strongest first: the admin file (`/etc/agent-keepalive/config.json`, `%ProgramData%\agent-keepalive\config.json`, `/Library/Application Support/agent-keepalive/config.json`), whose `locked` list freezes keys; environment (`AGENT_KEEPALIVE_METRICS_URL`, `AGENT_KEEPALIVE_METRICS=off`); the user's settings; the profile.

## Usage metrics

On by default (opt-out), per the profile. Reports are pseudonymous: a random install id (UUID v4, generated locally, stored in the data dir, reset with `agent-keepalive metrics reset-id`) is the only identifier. Never sent: names, paths, project names, titles, session ids, prompts, host names. See `docs/metrics.md` for the exact fields. Turning reporting off stops it immediately and drops anything queued.

## Updates

| Install kind (recorded at install) | UI notification | Action |
|---|---|---|
| Windows setup, Windows portable, AppImage, archive | "vX is available" + **Update** | download, verify signature + sha256, swap the binary (the portable exe carries its window; the setup replaces both), restart; the UI reconnects |
| deb, rpm, arch, Homebrew | "vX is available" + the package-manager command (`apt`, `dnf`, `pacman`, `brew upgrade`) | packages register the signed repository (Homebrew: the tap); the package manager updates |
| macOS pkg | **Update** | download + verify the new pkg, open Installer |
| supervised (`--no-update`) | none | |

The feed (`latest.json`) lists every artifact with its sha256 and is signed with Ed25519; the public key is in the profile. Checks run at start and every 6 hours.

## Releases

A `v*` tag runs `.github/workflows/release.yml`: it builds the UIs, builds the desktop window on Linux (x86_64 and aarch64 runners), Windows and macOS, cross-compiles six targets, packages deb/rpm/arch (nfpm), AppImage (x86_64, aarch64), Windows setup and portable `.exe` (NSIS, built on Linux), macOS `.pkg` (on a macOS runner), a Homebrew formula pushed to the `gurupras/homebrew-tap` tap, plus `.tar.gz`/`.zip` archives and `SHA256SUMS`. It creates the GitHub Release, publishes the hub image to `ghcr.io`, uploads everything to the downloads host, signs and promotes the apt/rpm/arch repositories, and moves `latest.json` last.

## Scheduler rules

These are requirements, each with a test:

1. **Timing is anchored on the request.** The next ping is due `TTL − margin` after the `Anchor()` (request time) of the last API call of any kind, never after the time its response was recorded.
2. **Delivery happens outside the scheduler lock.** A tick decides under the lock, delivers without it (each session has at most one delivery in flight; deliveries to different sessions run concurrently, each with a timeout), and commits the result under the lock again. API reads never wait on a delivery.
3. **One unconsumed ping per session.** A ping is recorded as `sending` before delivery is attempted, then `delivered`, `seen` (its marker is in the transcript), `verified` (its reply was read). No new ping goes to a session while an earlier one is `sending` or `delivered`; the plan shows `blocked` instead. A delivered ping never seen within the verify window becomes `lost`, and the plan is paused with a reason rather than retried.
4. **Queued pings never outlive their session.** The Codex deliverer implements `model.Withdrawer`; the scheduler sweeps at start-up and every minute, removing every queued marker row whose thread is not live or that is older than the verify window.
5. **Switching conversation ends the plan.** A plan belongs to one conversation. When the process behind it runs another session (`/clear`, `/resume`), the plan ends (`EndSwitched`). Deliverers re-check the pid → session mapping immediately before writing and return `ErrSessionChanged`; `PingNow` applies the same check.
6. **Cold pings teach the TTL.** A cold ping lowers the learned TTL for that harness and model (stored, never below 5 minutes, at most halving), and the plan continues at the shorter interval. A second consecutive cold ping pauses the plan (`PlanPaused`, with the reason).
7. **"Saved" needs a cache hit.** A return is booked as saved only when the returning call's `CacheRead` is at least 80% of the context before the gap; the avoided cost is computed from that `CacheRead`. Otherwise the segment is booked as wasted with `EndMissed`.
8. **The ledger is durable and idempotent.** Open segments are rows. Closing a segment writes the ledger entry and updates the plan in one transaction; ledger entries are unique on `(plan_id, idle_from, ended_by)`. Restarts resume verification of unsettled pings and correct their cost.
9. **Busy is bounded.** Claude Code's session registry says `idle`, `busy`, `waiting` (a permission prompt or a question) or `shell` (the turn ended, and shell commands it started still run). `idle` and `shell` count as idle, since no model calls keep the cache warm in either; everything else counts as working. A session reported working for longer than 2 hours without new transcript activity, or whose transcript shows a new session start or resume, is treated as idle-but-stale: the plan shows `blocked` with the reason, and nothing is pinged until the transcript moves again.
10. **Wall-clock time only.** Times are taken with `time.Now().Round(0)` (no monotonic reading), so behaviour is the same before and after a restart. A jump of more than a minute between ticks (suspend, clock change) re-evaluates every plan's warm/cold state from the transcript before any ping.
11. **One scheduler per user.** The instance lock is per user and per agent config directory, not per data directory, so two servers never ping the same sessions; a second server proxies to the first.
12. **The ping text is fixed** (`model.PingText`). No API, setting or config changes it.

## Tailing rules

One shared tailer reads JSONL transcripts for both harnesses: it remembers offset, size and file identity (inode/file id), restarts from zero when the file is replaced or shrinks, keeps a partial last line until it is complete (bounded at 8 MiB, beyond which the line is skipped), and on first sight of a file larger than 64 MiB reads only its last 64 MiB. Dedup sets (message and response ids) are bounded. The first read of a large file happens off the scan path, so one huge transcript never delays pings for other sessions.
