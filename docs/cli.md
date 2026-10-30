# Command line

```
agent-keepalive <command> [flags]
```

Without a command, the desktop installs (deb, rpm, Arch, AppImage, Windows setup and portable, macOS `.pkg`) run `app`, which opens the desktop window, or `serve` on a Linux machine with no display. The archives, Homebrew and dev builds run `serve`. `agent-keepalive <command> -h` lists a command's flags.

To run the server and web UI without the window, name the command: `agent-keepalive serve` (in the foreground) or `agent-keepalive open` (in the background, with the UI in your browser).

## Commands

| Command | What it does |
|---|---|
| `serve` | Run the server in the foreground. |
| `app` | Start the server in the background if none is running, then open the desktop window. Without the window installed, or when it cannot start (no WebKitGTK 4.1, no WebView2 Runtime), it opens the UI in your browser. |
| `open` | Start the server in the background if none is running, then open the UI in your browser. |
| `mcp` | Serve the Model Context Protocol on standard input and output, so an agent can keep its own session, or a swarm, warm and check on it. Starts a background server if none is running. See [Agents and swarms](/agents). |
| `status` | Show whether a server is running, and where. |
| `stop` | Stop the running server. |
| `metrics on \| off \| status \| preview \| reset-id` | Usage reports: turn them on or off, show the current state, print the next report exactly as it would be sent, or start over with a new random install id. See [Usage reports and privacy](/metrics). |
| `update check \| apply \| status` | Look for a newer version, install it, or show the update state. On a package-manager install, `check` prints the command to run instead (`apt`, `dnf`, `zypper`, `pacman` or `brew`). |
| `version` | Print the version, the distribution profile and how it was installed. |

Every command takes `--data-dir`, and finds the running server through it.

### `serve` flags

| Flag | |
|---|---|
| `--port N` | Listen on exactly this port on 127.0.0.1. Default: `$PORT`, else 4344 or the next free one. |
| `--data-dir DIR` | State directory (below). |
| `--log-level LEVEL` | `debug`, `info`, `warn` or `error`. Default `info`. |
| `--pricing FILE` | Price table override. Default `<data dir>/pricing.json` ([format](/using#ledger)). |
| `--no-update` | Turn self-update off. |
| `--frame-ancestors SRC` | Pages allowed to frame the UI, as CSP `frame-ancestors` sources. Default `'self'`. |
| `--ui-addon DIR` | A directory served under `addon/`; its `addon.css` and `addon.js` are linked into the UI. |
| `--supervised` | Run under another program: no self-update, and no Quit in the UI. |
| `--exit-on-stdin-eof` | Stop when standard input closes, for a supervising program. |

### `app` flags

| Flag | |
|---|---|
| `--port N` | Port for a server this starts. |

`AGENT_KEEPALIVE_DESKTOP` names the window's executable, or `none` to open a browser instead.

### `mcp` flags

| Flag | |
|---|---|
| `--port N` | Port for a server this starts. |

### `open` flags

| Flag | |
|---|---|
| `--port N` | Port for a server this starts. |
| `--no-browser` | Print the URL instead of opening a browser. |

## Data directory

Plans, pings, the ledger and settings live in a SQLite database in the data directory, next to `config.json`, the usage-report install id (`install-id`) and an optional `pricing.json`.

| OS | Default |
|---|---|
| Linux and other Unixes | `$XDG_STATE_HOME/agent-keepalive`, else `~/.local/state/agent-keepalive` |
| macOS | `~/Library/Application Support/agent-keepalive` |
| Windows | `%LOCALAPPDATA%\agent-keepalive` |
| Windows portable | `agent-keepalive-data` next to the `.exe` |

`--data-dir` or `AGENT_KEEPALIVE_DATA_DIR` overrides it. Only one server per user does the pinging; a second one started with another data directory hands off to the first.

## Environment

| Variable | |
|---|---|
| `AGENT_KEEPALIVE_DATA_DIR` | The data directory. |
| `AGENT_KEEPALIVE_METRICS` | `on` or `off`: usage reports. |
| `AGENT_KEEPALIVE_METRICS_URL` | Send usage reports to this hub instead. |
| `AGENT_KEEPALIVE_METRICS_TOKEN` | Bearer token for a hub that requires one. |
| `AGENT_KEEPALIVE_FRAME_ANCESTORS` | Same as `--frame-ancestors`. |
| `PORT` | Same as `--port`, for `serve`. |
| `CLAUDE_CONFIG_DIR` | Where Claude Code keeps its state. Default `~/.claude`. |
| `CODEX_HOME` | Where Codex keeps its state. Default `~/.codex`. |

## Configuration files

Settings come from these layers, strongest first:

1. **The admin file**, for every user of the machine: `/etc/agent-keepalive/config.json` on Linux, `/Library/Application Support/agent-keepalive/config.json` on macOS, `%ProgramData%\agent-keepalive\config.json` on Windows. Its `locked` list freezes keys, so neither users nor their environment can change them.
2. The environment variables above.
3. **The user file**, `<data dir>/config.json`, which Settings writes.
4. The build's distribution profile.
5. Built-in defaults.

Both files use nested JSON, and keys are named by their dotted path:

```json
{
  "metrics": { "enabled": false },
  "locked": ["metrics.enabled"]
}
```

## Exit codes

| Code | |
|---|---|
| 0 | Success. |
| 1 | An error, printed on stderr. |
| 2 | Bad arguments; the usage was printed. |
| 3 | No server is running (`status`, `stop`). |
