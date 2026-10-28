import { reactive } from 'vue'
import type {
  Info, LedgerEntry, LedgerResponse, MetricsStatus, Missed, Plan, PingRecord,
  ServerEvent, SessionView, Settings, Totals, UpdateStatus,
} from './types'
import { ApiError, httpTransport, type LinkState, type Transport } from './transport'
import { errorText, toast, toastError } from '../composables/toasts'
import { compareVersions, sessionTitle } from '../lib/derive'
import { hoursLabel, money } from '../lib/format'
import { readStored, writeStored } from '../composables/persisted'

export const POLL_MS = 10_000
const MISSED_MS = 5 * 60_000
const PING_LIMIT = 50

export type LedgerRange = 'today' | '7d' | '30d' | '90d' | 'all'
export const ledgerRanges: { id: LedgerRange, label: string }[] = [
  { id: 'today', label: 'Today' },
  { id: '7d', label: '7 days' },
  { id: '30d', label: '30 days' },
  { id: '90d', label: '90 days' },
  { id: 'all', label: 'All time' },
]

export const emptyTotals = (): Totals => ({ saved: 0, wasted: 0, net: 0, pingCost: 0, pings: 0, returns: 0, expired: 0 })

/** UpdatePhase is what the UI is doing about an update, beyond the server's UpdateStatus. */
export type UpdatePhase = 'idle' | 'applying' | 'restarting'

function initialState () {
  return {
    /** the first load finished (successfully or not) */
    loaded: false,
    mock: false,
    link: 'connecting' as LinkState,
    /** the last load error, shown while there is no data */
    error: '',
    /** the user quit the server from the UI */
    quit: false,
    /** the server refused the API token (401): this page is stale or not from this server */
    authError: false,
    info: undefined as Info | undefined,
    sessions: [] as SessionView[],
    settings: undefined as Settings | undefined,
    metrics: undefined as MetricsStatus | undefined,
    update: undefined as UpdateStatus | undefined,
    updatePhase: 'idle' as UpdatePhase,
    ledger: {
      range: (readStored<LedgerRange>('ledger-range') ?? '30d') as LedgerRange,
      entries: [] as LedgerEntry[],
      totals: emptyTotals(),
      loading: false,
      error: '',
    },
    missed: undefined as Missed | undefined,
    missedError: '',
    /** ping history per session key, loaded when its drawer opens */
    pings: {} as Record<string, PingRecord[]>,
  }
}

const state = reactive(initialState())

/** store is the reactive app state. Treat it as read-only outside this module. */
export const store = state

let transport: Transport = httpTransport()
let unsubscribe: (() => void) | undefined
let pollTimer: ReturnType<typeof setInterval> | undefined
let missedTimer: ReturnType<typeof setInterval> | undefined
let ledgerTimer: ReturnType<typeof setTimeout> | undefined
/** the server version seen in the last hello frame of this page */
let lastVersion: string | undefined

/** useTransport replaces the backend (mock mode, Storybook). */
export function useTransport (t: Transport, mock = true) {
  transport = t
  state.mock = mock
}

/** resetStore puts the state back to a fresh page load (Storybook). */
export function resetStore () {
  unsubscribe?.()
  unsubscribe = undefined
  clearInterval(pollTimer)
  clearInterval(missedTimer)
  pollTimer = missedTimer = undefined
  lastVersion = undefined
  const mock = state.mock
  Object.assign(state, initialState(), { mock })
}

const enc = (key: string) => encodeURIComponent(key)

export function titleOf (key: string): string {
  const s = state.sessions.find(x => x.key === key)
  return s ? sessionTitle(s) : key
}

/** noteAuth records a refused token; true if err was one. */
function noteAuth (err: unknown): boolean {
  if (err instanceof ApiError && err.status === 401) {
    state.authError = true
    return true
  }
  return false
}

async function act<T> (what: string, fn: () => Promise<T>): Promise<T | undefined> {
  try {
    return await fn()
  } catch (err) {
    if (!noteAuth(err)) toastError(what, err)
    return undefined
  }
}

// ---- loading ---------------------------------------------------------------

export async function loadSessions () {
  state.sessions = (await transport.request<SessionView[] | null>('GET', 'sessions')) ?? []
}

function rangeFrom (r: LedgerRange, now = Date.now()): string | undefined {
  if (r === 'all') return undefined
  if (r === 'today') {
    const d = new Date(now)
    d.setHours(0, 0, 0, 0)
    return d.toISOString()
  }
  const days = Number.parseInt(r, 10)
  return new Date(now - days * 86_400_000).toISOString()
}

export async function loadLedger () {
  state.ledger.loading = true
  try {
    const from = rangeFrom(state.ledger.range)
    const r = await transport.request<LedgerResponse | null>('GET', 'ledger' + (from ? '?from=' + encodeURIComponent(from) : ''))
    state.ledger.entries = [...(r?.entries ?? [])].sort((a, b) => Date.parse(b.endedAt) - Date.parse(a.endedAt))
    state.ledger.totals = { ...emptyTotals(), ...r?.totals }
    state.ledger.error = ''
  } catch (err) {
    state.ledger.error = errorText(err)
  } finally {
    state.ledger.loading = false
  }
}

export function setLedgerRange (r: LedgerRange) {
  state.ledger.range = r
  writeStored('ledger-range', r)
  loadLedger()
}

export async function loadMissed () {
  if (!state.settings?.showMissed) {
    clearInterval(missedTimer)
    missedTimer = undefined
    state.missed = undefined
    return
  }
  missedTimer ??= setInterval(() => { loadMissed() }, MISSED_MS)
  try {
    state.missed = await transport.request<Missed>('GET', 'missed')
    state.missedError = ''
  } catch (err) {
    state.missedError = errorText(err)
  }
}

async function loadOptional<T> (path: string): Promise<T | undefined> {
  try {
    return await transport.request<T>('GET', path)
  } catch {
    return undefined
  }
}

/** refresh reloads everything; sessions also arrive over the WebSocket. */
export async function refresh () {
  try {
    const [sessions, settings, info, metrics, update] = await Promise.all([
      transport.request<SessionView[] | null>('GET', 'sessions'),
      transport.request<Settings>('GET', 'settings'),
      loadOptional<Info>('info'),
      loadOptional<MetricsStatus>('metrics'),
      loadOptional<UpdateStatus>('update'),
    ])
    state.sessions = sessions ?? []
    state.settings = settings
    if (info) state.info = info
    if (metrics) state.metrics = metrics
    if (update) setUpdate(update)
    state.error = ''
    state.authError = false
  } catch (err) {
    noteAuth(err)
    state.error = errorText(err)
  } finally {
    state.loaded = true
  }
  await Promise.all([loadLedger(), loadMissed()])
}

// ---- live events -----------------------------------------------------------

function patchPlan (p: Plan) {
  const s = state.sessions.find(x => x.key === p.key)
  const before = s?.plan
  if (s) s.plan = p
  if (before?.id === p.id && before.state === p.state) return
  const title = titleOf(p.key)
  if (p.state === 'paused') toast('error', `Keepalive paused: ${title}`, { body: p.reason, ms: 0 })
  else if (p.state === 'failed') toast('error', `Keepalive failed: ${title}`, { body: p.reason || p.lastError, ms: 0 })
  else if (p.state === 'ended' && before) toast('info', `Keepalive ended: ${title}`, { body: p.reason })
  else if (p.state === 'done' && before) toast('info', `Keepalive finished: ${title}`)
}

function upsertPing (p: PingRecord) {
  const list = state.pings[p.key]
  if (!list) return
  const i = list.findIndex(x => x.id === p.id)
  if (i >= 0) list[i] = p
  else list.unshift(p)
  list.sort((a, b) => Date.parse(b.sentAt) - Date.parse(a.sentAt))
  if (list.length > PING_LIMIT) list.length = PING_LIMIT
}

function onPing (p: PingRecord) {
  const before = state.pings[p.key]?.find(x => x.id === p.id)
  upsertPing(p)
  if (before?.state === p.state && before.cold === p.cold) return
  const title = titleOf(p.key)
  if (p.state === 'failed') toast('error', `Ping failed: ${title}`, { body: p.error })
  else if (p.state === 'lost') toast('error', `Ping lost: ${title}`, { body: p.error || 'It was delivered but never showed up in the transcript.' })
  else if (p.state === 'verified' && p.cold) {
    toast('error', `Cache was already cold: ${title}`, { body: `The ping re-wrote the context (${money(p.cost)}).` })
  }
}

function onLedger (e: LedgerEntry) {
  // Totals come from the server; reload once a burst of entries is over.
  clearTimeout(ledgerTimer)
  ledgerTimer = setTimeout(() => { loadLedger() }, 300)
  if (e.outcome === 'saved') toast('success', `Saved ${money(e.net)}: ${e.title || titleOf(e.key)}`)
}

function onHello (version: string, info: Info) {
  state.info = info
  const pending = readStored<{ from: string, to: string }>('pending-update')
  const newer = (lastVersion && compareVersions(version, lastVersion) > 0) ||
    (pending && compareVersions(version, pending.from) > 0)
  if (newer) {
    writeStored('pending-update', undefined)
    state.updatePhase = 'idle'
    toast('success', `Updated to ${version}`, {
      body: 'Reload to use the new version of this page.',
      action: { label: 'Reload', run: () => location.reload() },
      ms: 0,
    })
  } else if (state.updatePhase === 'restarting' && lastVersion === version) {
    state.updatePhase = 'idle'
  }
  lastVersion = version
}

export function onEvent (e: ServerEvent) {
  switch (e.type) {
    case 'hello': onHello(e.data.version, e.data.info); break
    case 'sessions': state.sessions = e.data ?? []; break
    case 'plan': patchPlan(e.data); break
    case 'ping': onPing(e.data); break
    case 'ledger': onLedger(e.data); break
    case 'settings':
      state.settings = e.data
      loadMissed()
      break
    case 'update': setUpdate(e.data); break
    case 'metrics': state.metrics = e.data; break
  }
}

function setLink (s: LinkState) {
  const was = state.link
  state.link = s
  if (s === 'open') {
    clearInterval(pollTimer)
    pollTimer = undefined
    if (was !== 'open' && state.loaded) refresh()
  } else if (s === 'down') {
    if (state.updatePhase === 'applying') state.updatePhase = 'restarting'
    // While the socket is down, poll the sessions so countdowns stay right.
    // The first poll runs at once: it tells a refused token (401) from a
    // server that is away.
    const poll = () => {
      loadSessions().then(() => {
        state.error = ''
        state.authError = false
      }, err => {
        noteAuth(err)
        state.error = errorText(err)
      })
    }
    if (!pollTimer) {
      poll()
      pollTimer = setInterval(poll, POLL_MS)
    }
  }
}

/** start loads everything and subscribes to live updates. */
export async function start () {
  if (new URLSearchParams(location.search).has('mock')) {
    const { mockTransport } = await import('../mock/server')
    useTransport(mockTransport())
  }
  await refresh()
  unsubscribe = transport.subscribe(onEvent, setLink)
}

// ---- plans and pings -------------------------------------------------------

export async function startPlan (key: string, hours: number): Promise<boolean> {
  const p = await act('Could not start keepalive', () =>
    transport.request<Plan>('POST', `sessions/${enc(key)}/plan`, { hours }))
  if (!p) return false
  patchPlan(p)
  toast('success', `Keeping ${titleOf(key)} warm for ${hoursLabel(hours)}`)
  return true
}

export async function keepIndefinitely (key: string): Promise<boolean> {
  const p = await act('Could not start keepalive', () =>
    transport.request<Plan>('POST', `sessions/${enc(key)}/plan`, { indefinite: true }))
  if (!p) return false
  patchPlan(p)
  toast('success', `Keeping ${titleOf(key)} warm until you stop it`)
  return true
}

export async function extendPlan (key: string, addHours: number): Promise<boolean> {
  const p = await act('Could not extend keepalive', () =>
    transport.request<Plan>('PATCH', `sessions/${enc(key)}/plan`, { addHours }))
  if (!p) return false
  // The server clamps the end to now + maxHours, so report what it granted.
  const before = state.sessions.find(x => x.key === key)?.plan?.until
  const granted = before ? (Date.parse(p.until) - Date.parse(before)) / 3_600_000 : addHours
  patchPlan(p)
  toast('success', granted > 0 && granted < addHours - 1e-6
    ? `Extended by ${hoursLabel(granted)}, the most allowed`
    : `Extended by ${hoursLabel(addHours)}`)
  return true
}

export async function resumePlan (key: string): Promise<boolean> {
  const p = await act('Could not resume keepalive', () =>
    transport.request<Plan>('PATCH', `sessions/${enc(key)}/plan`, { resume: true }))
  if (!p) return false
  patchPlan(p)
  toast('success', `Resumed keepalive: ${titleOf(key)}`)
  return true
}

export async function cancelPlan (key: string): Promise<boolean> {
  const ok = await act('Could not stop keepalive', async () => {
    await transport.request<unknown>('DELETE', `sessions/${enc(key)}/plan`)
    return true
  })
  if (!ok) return false
  const s = state.sessions.find(x => x.key === key)
  if (s?.plan) s.plan = { ...s.plan, state: 'cancelled' }
  toast('info', `Stopped keeping ${titleOf(key)} warm`)
  return true
}

export async function pingNow (key: string): Promise<boolean> {
  const r = await act('Ping failed', () => transport.request<PingRecord>('POST', `sessions/${enc(key)}/ping`))
  if (!r) return false
  upsertPing(r)
  toast('info', `Ping sent: ${titleOf(key)}`)
  return true
}

export async function loadPings (key: string) {
  try {
    const list = await transport.request<PingRecord[] | null>('GET', `sessions/${enc(key)}/pings?limit=${PING_LIMIT}`)
    state.pings[key] = [...(list ?? [])].sort((a, b) => Date.parse(b.sentAt) - Date.parse(a.sentAt))
  } catch (err) {
    toastError('Could not load the ping history', err)
  }
}

// ---- settings and metrics --------------------------------------------------

export async function saveSettings (s: Settings): Promise<boolean> {
  const r = await act('Could not save settings', () => transport.request<Settings>('PUT', 'settings', s))
  if (!r) return false
  state.settings = r
  toast('success', 'Settings saved')
  loadMissed()
  return true
}

export async function setMetricsEnabled (enabled: boolean): Promise<boolean> {
  const r = await act('Could not change usage reports', () => transport.request<MetricsStatus>('PUT', 'metrics', { enabled }))
  if (!r) return false
  state.metrics = r
  toast('success', enabled ? 'Usage reports turned on' : 'Usage reports turned off', {
    body: enabled ? undefined : 'Nothing more is sent, and queued reports were deleted.',
  })
  return true
}

export async function resetInstallId (): Promise<boolean> {
  const r = await act('Could not reset the install id', () => transport.request<MetricsStatus>('POST', 'metrics/reset-id'))
  if (!r) return false
  state.metrics = r
  toast('success', 'New install id', { body: 'Reports from now on carry the new id.' })
  return true
}

export function previewMetrics (): Promise<unknown> {
  return transport.request<unknown>('GET', 'metrics/preview')
}

// ---- updates ---------------------------------------------------------------

function setUpdate (u: UpdateStatus) {
  const prev = state.update
  state.update = u
  if (u.state === 'installing' && state.updatePhase === 'applying') state.updatePhase = 'restarting'
  if (u.state === 'error' && state.updatePhase !== 'idle') state.updatePhase = 'idle'
  if (prev?.state === 'downloading' && u.state === 'ready' && u.action === 'apply') state.updatePhase = 'restarting'
}

/** checkForUpdate asks the server to read the feed now; returns the status. */
export async function checkForUpdate (): Promise<UpdateStatus | undefined> {
  const r = await act('Could not check for updates', () => transport.request<UpdateStatus>('POST', 'update/check'))
  if (!r) return undefined
  setUpdate(r)
  if (!r.enabled) toast('info', 'Updates are managed outside agent-keepalive in this installation')
  else if (r.state === 'error') toast('error', 'Update check failed', { body: r.error })
  else if (r.state !== 'available' || !r.latest) toast('success', `agent-keepalive ${r.current} is up to date`)
  return r
}

export async function applyUpdate (): Promise<boolean> {
  const from = state.update?.current ?? state.info?.version ?? ''
  const to = state.update?.latest?.version ?? ''
  state.updatePhase = 'applying'
  try {
    const r = await transport.request<UpdateStatus>('POST', 'update/apply')
    writeStored('pending-update', { from, to })
    setUpdate(r)
    return true
  } catch (err) {
    state.updatePhase = 'idle'
    if (state.update) state.update = { ...state.update, state: 'error', error: errorText(err) }
    return false
  }
}

// ---- lifecycle -------------------------------------------------------------

export async function quit (): Promise<boolean> {
  const ok = await act('Could not stop agent-keepalive', async () => {
    await transport.request<unknown>('POST', 'quit')
    return true
  })
  if (!ok) return false
  unsubscribe?.()
  unsubscribe = undefined
  clearInterval(pollTimer)
  clearInterval(missedTimer)
  pollTimer = missedTimer = undefined
  state.quit = true
  state.link = 'down'
  return true
}
