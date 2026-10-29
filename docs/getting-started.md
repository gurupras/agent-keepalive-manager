# Getting started

agent-keepalive is a small local server with a desktop window and a web UI. It finds the Claude Code and Codex CLI sessions running on your machine and, for the ones you choose, keeps their provider prompt cache from expiring while you are away.

## Why a warm cache matters

Every turn of a long agent conversation resends the whole context. Providers cache that context, and a turn that hits the cache is billed at the cheap cache-read price. The cache lives only for a short time after the last request, though: 5 minutes or 1 hour for Claude, depending on the tier, and about 30 minutes for Codex. Come back after it has expired and the next turn writes the entire context to the cache again, at the full cache-write price (for Claude, 1.25× or 2× the input price).

For a session with a large context, that one re-write can cost more than a long run of tiny "Reply only: pong" turns that each read the cache and reset its timer. agent-keepalive sends those turns for you, just before the cache would expire, and only for as long as it pays off.

## Install and open

Install it for your platform ([Installing](/install)), then:

```sh
agent-keepalive
```

This starts the server in the background if it is not already running and opens the agent-keepalive window. The desktop entry, the Start-menu shortcut and the macOS app do the same thing. The server listens on `127.0.0.1` only, on port 4344 or the next free one, and keeps running when you close the window; **Quit** in the window's menu stops it.

Prefer a browser? `agent-keepalive open` starts the server the same way and opens the UI in your browser instead. `agent-keepalive serve` runs only the server, in the foreground. The `.tar.gz`/`.zip` archives and Homebrew have no window: there, `agent-keepalive` runs `serve`, and `agent-keepalive open` is how you open the UI.

## Keep a session warm

1. **All projects** lists every project with a live Claude Code or Codex session; open one, or **Sessions**, to see each session with its context size and a countdown to when its cache expires.
2. Pick a session and choose how long to keep it warm: one of the presets, a custom number of hours, or **until you stop it**, a rolling plan that every prompt you type extends by another full window. The panel shows what the plan will cost at most before you confirm.
3. Walk away. Shortly before the cache would expire, agent-keepalive sends a ping and checks the reply in the transcript. It never pings while the session is working, and it never sends anything but its fixed ping text, which carries a `[keepalive id=…]` marker.
4. When you come back and type, the plan carries on in the background. It ends when its time runs out, when you stop it, when the session exits, or when you switch that terminal to another conversation.

Plans are capped at the session's **break-even**: the point where the pings would have cost more than one re-write of the cache. Past that, letting the cache expire is cheaper, and the UI says so.

## See what it saved

The **Ledger** view books every idle period:

- **Saved**: you came back while the cache was warm. The amount is the re-write you avoided minus what the pings cost.
- **Wasted**: the plan ended, or the session exited, before you came back. The amount is what the pings cost.

**Missed** (turn it on in Settings) is a what-if over your recent transcripts: what keeping every idle session warm would have saved or wasted.

## Run it at login

- **Linux** packages ship a user service: `systemctl --user enable --now agent-keepalive`.
- **Windows**: tick *Start at login* in the setup installer. An upgrade keeps your choice.
- **macOS**: add *agent-keepalive* (in Applications) to System Settings › General › Login Items.
- Anywhere else, run `agent-keepalive serve` from your own startup script.

## Next

- [Using the UI](/using): plan states, pinging by hand, settings.
- [Command line](/cli): every command, flag and environment variable.
- [Usage reports and privacy](/metrics): what the public build reports, and how to turn it off.
- [How it works](/architecture): discovery, delivery and the scheduler's rules.
