# Using the UI

`agent-keepalive` opens the UI in its window (`agent-keepalive open` opens it in your browser, at `http://127.0.0.1:<port>/`). The sidebar lists the views (**All projects**, **Sessions**, **Ledger**, and **Missed savings** when you turn it on in Settings) and every project with a live session. A dot shows each project's state: solid when something in it is kept warm, faded when something is warm, cool when everything is cold. Press <kbd>/</kbd> to filter the projects, and **Pin** a project on its page to keep it at the top. In a narrow window the sidebar folds behind the menu button. Everything updates live.

Each view opens with the same strip of numbers: what the ledger booked as saved and wasted in its current range, how many sessions are kept warm and warm, and what re-caching every warm session would cost.

## Projects

**All projects** has one row per project: its sessions, the warm session nobody keeps warm that goes cold next, and the value of its warm cache. Projects with only cold sessions are folded away.

A project's page lists its kept and warm sessions; the cold ones are folded at the bottom, since their next message re-caches either way. **Sessions** lists every session across all projects, with a search box.

## Sessions

Sessions show as a table or as cards (the toggle in the page header). Kept sessions come first, then working ones, then the ones soonest to go cold. Each shows:

- the session's title, its folder when that is below the project root, and Claude Code's own recap of the session when it has written one;
- its context size; hover the info icon for what a cold return would cost (re-writing the whole context to the cache), what each ping costs, and the model;
- the cache state: a countdown while the cache is warm, *working* while the agent is busy, or cold;
- its plan, or **Keep warm**.

Click a title for the details: process, delivery channel, last API call, when you last typed, the ping interval, the plan, and the session's ping history.

A session shows **Can't be pinged** when agent-keepalive has no way to reach it: a Claude Code session without a messaging socket, or a Codex session that cannot take queued messages. Discovery still works for it, but no plan can start.

## Plans

**Keep warm** opens the plan panel:

- **Presets and custom.** Choose a number of hours, or type one (at least 15 minutes).
- **Until you stop it.** A rolling plan: each prompt you type moves its end one full window past that prompt.
- **The cap.** No plan runs longer than *Longest plan* in Settings (36 hours unless you change it), and no plan runs past the session's break-even, where the pings would cost more than re-writing the cache once. The panel says which limit applies.
- **The quote.** Before you confirm, the panel shows how many pings the plan can send and what they would cost at most.

A running plan can be **extended** (in steps up to the cap), **stopped**, or sent a ping **now**. Its status is one of:

| Status | Meaning |
|---|---|
| Kept warm | Active: the next ping is scheduled `TTL − margin` after the last API call. |
| Next ping once the session is idle | The agent is working; nothing is sent until it stops. |
| Blocked | An earlier ping has not been consumed yet, or the session looks stuck (reported working for over two hours with no transcript activity). The reason is shown. |
| Paused | Two pings in a row found the cache cold, or a ping was delivered but never appeared in the transcript. **Resume** starts it again. |
| Done, stopped, ended, failed | The plan ran out, you stopped it, the session exited or switched conversation (`/clear`, `/resume`), or a ping could not be delivered before the cache expired. |

A ping that finds the cache cold teaches agent-keepalive a shorter TTL for that tool and model (never below 5 minutes), and the plan continues at the shorter interval.

## Ledger

The ledger books every idle period of a kept session, over the range you pick:

| | |
|---|---|
| **Saved** | You came back while the cache was warm, and your next turn read at least 80% of the context from the cache. The amount is the cache write avoided minus the read paid. |
| **Wasted** | The plan ran out, was stopped, or the session exited before you came back; or a ping found the cache cold; or your return missed the cache. The amount is what the pings cost. |
| **Net** | Saved minus wasted. |
| **Pings** | How many were sent and what they cost. |

Amounts use the provider's list prices. To use your own (a negotiated rate, or a model the table does not know yet), put a `pricing.json` in the [data directory](/cli#data-directory):

```json
{
  "prices": [
    { "name": "My model", "match": "my-model", "input": 3, "output": 15, "cacheRead": 0.3, "cacheWrite5m": 3.75, "cacheWrite1h": 6 }
  ]
}
```

Prices are USD per million tokens. `match` is a regular expression tested against the model id. Your entries are tried before the built-in table; add `"replace": true` to use only yours.

## Missed

With *Show missed savings* on in Settings, this view replays your recent transcripts as if every idle session had been kept warm, and shows what that would have saved and wasted. Use it to decide which kinds of sessions are worth a plan.

## Settings

| Setting | |
|---|---|
| Longest plan (hours) | No plan runs longer than this. |
| Safety margin (minutes) | How long before the cache would expire a ping is sent. |
| Scan every (seconds) | How often sessions and transcripts are read. |
| Show missed savings | Turns on the Missed view. |
| Theme | System, light or dark. |
| Usage reports | Whether the daily usage report is sent, and a preview of it. See [Usage reports and privacy](/metrics). |

The sidebar's header has Settings, a light/dark switch, and a menu with **Check for updates**, **About** and **Quit**, which stops the server (click it twice).
