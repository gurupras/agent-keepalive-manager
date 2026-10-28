# agent-keepalive web UI

The local UI: Vue 3, Vite, Tailwind 4 and TypeScript. `npm run build` writes
it to `dist/`, which `embed.go` embeds into the `agent-keepalive` binary
(only `dist/.gitkeep` is committed).

```sh
npm ci
npm run dev              # http://localhost:5173, proxies api/ to 127.0.0.1:4344
npm run mock             # the same, against the in-browser mock backend
npm run build            # typecheck + build into dist/
npm run lint             # eslint (neostandard + eslint-plugin-vue)
npm run typecheck        # vue-tsc
npm run storybook        # http://localhost:6006
npm run build-storybook  # static Storybook in storybook-static/
```

## Development against a server

`npm run dev` proxies `api/` (HTTP and the WebSocket) to `KEEPALIVE_API`
(default `http://127.0.0.1:4344`) and rewrites `Origin` to the backend's,
since the server refuses cross-origin state changes and upgrades. The server
normally puts its API token into the page; the dev server injects the same
`<meta name="ka-token">` from `KEEPALIVE_TOKEN`, or from the `token` file in
`$AGENT_KEEPALIVE_DATA_DIR`, `$XDG_STATE_HOME/agent-keepalive` or
`~/.local/state/agent-keepalive` (macOS and Windows locations are tried too).

## Mock backend

Append `?mock=1` (`http://localhost:5173/?mock=1#/`). The app then runs
against `src/mock/server.ts`: synthetic sessions in several projects (kept,
indefinite, paused, blocked, working, stale, cold, unreachable, unpriced),
plans that ping through sending → delivered → seen → verified, a return that
books a saved ledger entry, an update that downloads and "restarts" into a new
version, usage reports, and Quit. Pick a scenario with extra parameters:

| Parameter | Values |
|---|---|
| `update` | `apply` (default), `command`, `download`, `none` |
| `metrics` | `on` (default), `off`, `admin`, `profile` (locked), `none` (no hub: section hidden) |

Storybook uses the same backend frozen at a fixed instant (`src/mock/stories.ts`),
so stories render identically on every run and their buttons still work.

## Constraints

- Relative URLs only (`./api/...`, Vite `base: './'`) and hash routing, so
  the app works under any path prefix and inside an iframe.
- No inline scripts or styles: the page runs under
  `script-src 'self'; style-src 'self'`. Vite's module-preload polyfill and
  asset inlining are off. The few dynamic values (cache bars) are set through
  the CSSOM, which CSP allows.
- Icons are bundled at build time from `@iconify-json/*`: list every icon in
  `src/icons/names.ts`. The offline Iconify build has no API client.
- Layout works from a 320px side panel to a full window (container queries).

## API use

Types in `src/api/types.ts` mirror the JSON of `internal/model` and
`internal/apitypes`; routes are in `docs/api.md`.

- The token is read from `<meta name="ka-token">` and sent as
  `X-Keepalive-Token` on every request; every `POST`/`PUT`/`PATCH`/`DELETE`
  carries `Content-Type: application/json`, with or without a body.
- Live events come over `api/ws?token=…` (ws: or wss: from the page URL).
  The link counts as up when the `hello` frame arrives; on close it reconnects
  with backoff from 1 s doubling to 30 s, and polls `api/sessions` every 10 s
  meanwhile. The header shows the link state.
- A 401 means the token was refused (the page is stale or not from this
  server); the UI says so and offers a reload.

## Theming

The palette is a set of `--ka-*` CSS variables on `:root`, defined in
`src/style.css` for light and dark. Tailwind's colours (`bg-surface`,
`text-ink-2`, `border-line`, `bg-accent`, `text-danger`, …) are built on them,
so overriding the variables restyles everything. Both definitions are wrapped
in `:where()` and have no specificity, so a host stylesheet (for example the
`addon.css` of `--ui-addon`) wins with plain selectors:

```css
:root { --ka-accent: #7c3aed; --ka-radius: 0.375rem; }          /* both schemes */
:root[data-ka-scheme="dark"] { --ka-bg: #000; }                 /* dark only */
```

| Variable | Use |
|---|---|
| `--ka-bg` | page background |
| `--ka-surface-1`, `--ka-surface-2`, `--ka-surface-3` | cards and dialogs; hover and chips; tracks and pressed states |
| `--ka-text-0` … `--ka-text-3` | text, strongest to faintest (`-3` is decorative only) |
| `--ka-border` | borders and dividers |
| `--ka-accent`, `--ka-on-accent` | primary actions and focus rings; text on the accent |
| `--ka-ok`, `--ka-warn`, `--ka-danger`, `--ka-info` | saved, attention, errors and waste, in-progress |
| `--ka-warm`, `--ka-cold` | kept-warm plans; expired caches |
| `--ka-shadow` | popovers, toasts and dialogs |
| `--ka-radius` | card and dialog corners |
| `--ka-font`, `--ka-mono` | text and code font stacks |

Soft backgrounds (`bg-accent-soft`, `bg-danger-soft`, …) are mixed from these
with `--ka-surface-1`.

The scheme is, strongest first: the user's choice in Settings › Appearance
(kept in `localStorage`), the embedding page's `<html data-theme="light|dark">`,
then `prefers-color-scheme`. The UI writes the result to
`<html data-ka-scheme="light|dark">`; style against that attribute, not
`data-theme`.
