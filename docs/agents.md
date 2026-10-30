# Agents and swarms (MCP)

`agent-keepalive mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server. Add it to Claude Code, Codex or any MCP client, and the agent itself can keep its session warm ("keep this alive for 24 hours"). An agent that coordinates other agents can also keep them warm and check how they are doing.

It runs over standard input and output and talks to the local server through the [local API](/api), with the API token from the data directory.

The server does the tracking; the MCP server only asks it. The first tool call from any agent starts the server in the background if none is running. The server is a separate program that keeps running after that agent exits, and every later call, from any agent, uses it. When several agents make their first call at once, a lock in the data directory makes one of them start the server while the others wait for it.

After its first call, each agent's MCP server follows the server's WebSocket, which pushes the session list whenever a scan changes it. Tool calls answer from that copy in about a millisecond, and `wait_for_agents` wakes on the push instead of polling. Process ancestry is read once per process. If the user stops the server, the MCP servers do not restart it until a tool call needs it.

A server started this way stops by itself once it has gone 30 minutes unused while no plan is active. The MCP servers' subscriptions do not count as use, so a swarm of idle agents with nothing kept warm does not hold it up; their next tool call starts it again.

## Setting it up

Claude Code, for every project:

```sh
claude mcp add --scope user agent-keepalive -- agent-keepalive mcp
```

Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.agent-keepalive]
command = "agent-keepalive"
args = ["mcp"]
tool_timeout_sec = 330   # wait_for_agents waits up to 300 s by default
```

Other clients: run `agent-keepalive mcp` as a stdio server. It takes `--data-dir` and `--port` like `open`.

## Which session is "this one"

The agent's harness starts the MCP server as a child process, so the server walks up its own process ancestry to the session's process. That session is `self`. Where ancestry is not visible, it falls back to the one session running in the server's working directory. If neither works, the tools that need `self` say so and ask for the session keys.

## Tools

| Tool | What it does |
|---|---|
| `keep_alive` | Keep the target sessions warm for `hours`, or `untilStopped`. With no target, it acts on the calling session. A session that already has a plan is extended, never shortened, and a paused plan is resumed. |
| `stop_keep_alive` | End the target sessions' plans. |
| `list_sessions` | The live sessions, with project, status, cache expiry and plan. `self` and `spawnedByMe` are marked. |
| `agent_status` | How the target sessions are doing, worst first (below). |
| `wait_for_agents` | Block until a target session changes state, or until all are idle (`until: "all_idle"`). Then return what changed and the status. |
| `savings` | What keeping warm has saved and wasted, overall and per live session. |

A fixed-hours plan still ends at the session's break-even point, where pings would cost more than re-writing the cache ([how it works](/architecture)). `keep_alive` says so when the plan ends earlier than asked. `untilStopped` rolls forward with each human prompt instead.

### Picking sessions

Every tool that acts on sessions takes a `target`:

| Field | Adds or narrows |
|---|---|
| `self` | adds the calling session |
| `sessions` | adds these keys (`claude:<id>`, `codex:<id>`) or bare ids |
| `spawnedByMe` | adds sessions whose agent process descends from the caller's, for example agents started from its shell or in a tmux server it launched |
| `all` | adds every live session |
| `project` | narrows to sessions in this repository or below it |
| `cwdPrefix` | narrows to sessions whose working directory is here or below |
| `titleContains` | narrows to sessions whose title contains this text, in any case |

If only narrowing fields are given, they narrow all sessions. An empty target means `self`. A coordinator usually passes `{"spawnedByMe": true}`, or its agents' keys.

### Agent status

`agent_status` gives each session a state:

| State | Meaning |
|---|---|
| `working` | in a turn, making model calls |
| `idle` | finished its turn and waits for a prompt; `backgroundCommands` is set when shell commands it started are still running, and it may wake when they finish |
| `waiting` | stopped on a permission prompt or a question, until someone answers in its terminal |
| `stuck` | in a turn but no model call for `stuckAfterMinutes` (default 20): a hung command, a long build, or a prompt the harness does not report |
| `exited` | asked for by key, but no longer live |

It also lists `problems` with a code and a sentence: `needs_input`, `no_progress`, `unreachable`, `cache_expiring` and `cache_cold` (idle with no plan), `plan_failed`, `plan_paused`, `plan_blocked`, `ping_error`. The latest summary the harness wrote for the session (`summary`), the time since its last model call and last prompt, and its context size come with each session. `needsAttention` lists the sessions with problems.

## Example: a coordinator

> Keep my agents warm until I stop you, and tell me when one needs me.

The coordinator calls `keep_alive {"target": {"spawnedByMe": true}, "untilStopped": true}`. Then it loops on `wait_for_agents {"target": {"spawnedByMe": true}}`. On each return it reads the `changes` and the `status`, unblocks or re-tasks the agents listed in `needsAttention`, and waits again. When the work is done, `stop_keep_alive` with the same target ends the plans.
