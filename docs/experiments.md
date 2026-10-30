# Experiments

Some choices about how agent-keepalive behaves can only be settled with data from many installs. For these, a release ships **experiments**: in some situations an install tries one of two behaviours, and the results go to the hub in the daily usage report ([metrics.md](metrics.md)). The next release then picks the better one for everyone.

Experiments are defined in the release (`internal/experiment`). Nothing about them is configured remotely, and the hub cannot start, stop or change one.

## Taking part, and opting out

Taking part is **on by default**, and experiments run only while usage reports are on and go to a hub. Without the reports, the results would reach no one.

Turn it off with any of these:

- Settings › Usage reports › **Take part in experiments**
- `agent-keepalive experiments off` (`on` and `status` work too)
- `AGENT_KEEPALIVE_EXPERIMENTS=off`
- the admin config, for every user of a machine: `{"experiments": {"enabled": false}, "locked": ["experiments.enabled"]}`

Turning usage reports off stops experiments too. Turning experiments off takes effect at once. An episode in progress is dropped, and nothing about experiments goes into later reports, including episodes that ended earlier that day and reports already waiting in the queue.

## Current experiments: pinging quiet sessions

Usually agent-keepalive never pings a session that is working, because a working session's own API calls keep its cache fresh. Some sessions are reported as working while their main agent makes no API calls, so their cache cools as if they were idle. These sessions are **quiet**. Claude Code's session registry stays `busy`:

| Cause | When | Experiment |
|---|---|---|
| `subagents` | the main agent's turn ended and background sub-agents still run | `busy-ping-subagents-1` |
| `tool` | the main agent waits on a long tool call: a build, a foreground sub-agent, an MCP call | none |
| `waiting` | a permission prompt or a question waits for the user | none |
| `other` | the session stays busy for another reason (hooks, teammates) | none |

Only `subagents` gets an experiment, because it is the one cause known to come after the main agent's turn ended. In the others the turn may still be running, and Claude Code hands a message that arrives then to the running turn, so a ping would reach the agent in the middle of its task instead of starting a turn of its own. Quiet sessions of every cause still show their cache countdown.

Each experiment compares two **arms** on its cause:

- **hold**: today's behaviour. No pings while the session is quiet.
- **ping**: the session is pinged as if it were idle.

It is not obvious which is cheaper. A ping keeps the cache warm for the agent's return, but costs a cache read, and the agent may come back before the cache would have expired anyway. A sub-agent finishing also triggers a new turn of its own, which may come before the cache would have expired. Each cause gets its own experiment, so later ones can run side by side.

### Detecting a quiet session

Claude Code 2.1.300 and later write an `api-request` record to the main transcript each time the main agent sends a request. A session is quiet when all of these hold:

- the registry says it is working;
- the transcript has `api-request` records, since older versions are never treated as quiet;
- no request of the main agent is in flight;
- it has sent or received nothing for at least a minute.

The cause is then `waiting` if the registry says so. It is `tool` if the last call stopped for a tool that has not answered, and `subagents` if a sub-agent transcript written since the process started shows the sub-agent still running. A sub-agent is running when its last call stopped for a tool, when a prompt or tool result waits for its next call, or when a request is in flight. It is done when its turn ended, when it handed back its report, or when the user stopped it. Any other quiet session is `other`.

In the UI, a quiet session's cache counts down as an idle session's does, and its status says what it waits on.

### Episodes

An **episode** is one quiet stretch of a session that has an active keepalive plan. It opens when the session turns quiet while its cache is still warm and a ping could reach it, and it is assigned to an arm at random: a hash of a random salt made on this install, the session and when the stretch began. It closes:

- `returned`: when the main agent makes its next real API call. Whether that call read the cache (80% of the context or more, as for the ledger) and what its cache writes cost are recorded.
- `left`: when the session goes idle without such a call (after a minute), or works without being quiet for 10 minutes. If the cache had expired by then, the cost of re-writing it is recorded, since the next return will pay it.
- `ended`: when the plan or the session ends first.

An episode cut off by a restart or by opting out is not reported.

The cost of an episode is its pings plus the cache writes that ended it. The report sums episodes per experiment, arm, harness and model ([metrics.md](metrics.md#experiments)). The hub compares the arms' mean cost per episode, with a confidence interval.

## Guards on spend

The ping arm spends money, so it is bounded in code:

- Only sessions with an **active keepalive plan** take part, so only sessions you already asked to keep warm.
- An episode sends at most the **break-even** number of pings for its model. That is the number whose cost equals one re-write of the cache, the same bound a plan's length has.
- An install sends at most **20 treatment pings and $1 of them per UTC day**, and opens at most **50 episodes**. A ping counts against the $1 from when it is sent, at the price of reading its context, until its reply gives the real cost, and is sent only if that fits. A ping that finds the cache cold costs a re-write, which the agent's return would otherwise have paid. Once a limit is reached, no new episode opens that day in either arm, so the arms stay comparable, and open episodes send no more pings.
- **Stop rule:** once 5 treatment episodes of an experiment have closed on an install, and together they cost more than re-writing the cache every time would have, the experiment stops on that install for good.
- A treatment ping that is never seen in the transcript stops the treatment for that episode. Unlike other pings, it does not pause the plan. A treatment ping that finds the cache cold does not shorten the learned cache lifetime or count towards pausing the plan, since a busy session may have held it.
- The treatment never fails or pauses a plan. If its session cannot be reached, a delivery fails or the cache expires anyway, the treatment stops for the episode and the plan carries on as for any working session.

Treatment pings are marked "experiment" in a session's ping history. The session drawer shows the open episode and its arm.

## Deploying

Reports with experiments are schema 2. A hub older than schema 2 refuses them, and the client then drops them, so upgrade the hub before releasing clients that send them ([metrics.md](metrics.md#hub-api)). A report without experiments is still schema 1.
