// Storybook helpers: a frozen mock backend whose state is copied into the
// store synchronously, so every story renders the same thing on every run.
// Buttons still work: actions go through the store to the mock backend.
import { resetStore, store, useTransport } from '../api/store'
import { toRow, type Row } from '../lib/derive'
import { clearToasts } from '../composables/toasts'
import { pinned, ui, viewMode } from '../composables/ui'
import { FROZEN_NOW, MockBackend, type MockOptions } from './server'

export let backend = new MockBackend({ now: FROZEN_NOW, simulate: false, latency: 200 })

/** seed resets the app state and fills it from a fresh frozen backend. */
export function seed (opts: MockOptions = {}) {
  backend = new MockBackend({ now: FROZEN_NOW, simulate: false, latency: 200, ...opts })
  resetStore()
  useTransport(backend.transport())
  clearToasts()
  Object.assign(ui, { drawer: null, planFor: null, settings: false, about: false, nav: false, search: '' })
  viewMode.value = 'table'
  pinned.value = {}
  const copy = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
  store.loaded = true
  store.link = 'open'
  store.info = copy(backend.info)
  store.sessions = copy(backend.sessions)
  store.settings = copy(backend.settings)
  store.metrics = copy(backend.metrics)
  store.update = copy(backend.update)
  const entries = copy(backend.ledger)
  store.ledger.entries = entries
  store.ledger.totals = copy((backend.route('GET', 'ledger', undefined) as { totals: typeof store.ledger.totals }).totals)
  store.missed = copy(backend.missed(FROZEN_NOW - 7 * 86_400_000))
  for (const s of backend.sessions) store.pings[s.key] = copy(backend.pings.filter(p => p.key === s.key).sort((a, b) => Date.parse(b.sentAt) - Date.parse(a.sentAt)))
}

/** row returns the fixture session with this title as a Row at the frozen time. */
export function row (title: string): Row {
  const s = store.sessions.find(x => x.title === title)
  if (!s) throw new Error(`no fixture session "${title}"`)
  return toRow(s, FROZEN_NOW)
}

export const TITLES = {
  kept: 'Ping verification against transcripts',
  working: 'Settings dialog layout',
  idle: 'Ledger idempotency tests',
  indefinite: 'Fix resize flicker on wayland',
  cold: 'Rewrite keybinding docs',
  paused: 'Retry budget for webhooks',
  blocked: 'Migrate ledger to decimal amounts',
  unreachable: 'One-off backfill script',
  stale: 'Neovim LSP config',
  unknownPrice: 'Shell prompt timing',
} as const

// Seed once at import, so stories can build their args from fixtures.
seed()
