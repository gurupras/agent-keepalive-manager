// In-browser stand-in for the Go server, enabled with ?mock=1. It serves the
// fixtures in the real API shapes and, unless frozen, simulates the server:
// pings go through sending → delivered → seen → verified, the user comes
// back to a kept session, updates download and "restart", Quit stops it.
//
// Extra query parameters pick a scenario:
//   update=apply|command|download|none   what the update toast offers
//   metrics=on|off|admin|profile|none    usage reports state (admin/profile: locked)
import type {
  Costs, Info, LedgerEntry, MetricsStatus, Missed, Plan, PingRecord, PingState,
  ServerEvent, SessionView, Settings, Totals, UpdateAction, UpdateStatus,
} from '../api/types'
import { ApiError, type LinkState, type Transport } from '../api/transport'
import { fixtures, HOME, ledgerFixtures } from './fixtures'

const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const iso = (ms: number) => new Date(ms).toISOString()

/** A fixed instant for Storybook and screenshots: 2026-10-27 14:30 UTC. */
export const FROZEN_NOW = Date.UTC(2026, 9, 27, 14, 30, 0)
export const MOCK_VERSION = '1.4.0'
export const MOCK_NEXT_VERSION = '1.5.0'

// USD per 1M tokens: [cache read, cache write at the session's tier, output]
const prices: [RegExp, number, number, number][] = [
  [/opus/, 0.5, 10, 25],
  [/sonnet/, 0.3, 6, 15],
  [/haiku/, 0.1, 2, 5],
  [/codex/, 0.125, 1.25, 10],
]

const NOTES = `## What's new

- **Faster verification**: pings are matched against the transcript as soon as it is written.
- The ledger can be filtered by range, and shows what each return saved.
- Usage reports can be previewed in *Settings › Usage reports*.

### Fixes

- A session that ran \`/clear\` no longer keeps its old plan.
- Countdown no longer drifts after the machine wakes from sleep.

Full changelog: https://github.com/gurupras/agent-keepalive/releases`

export interface MockOptions {
  now?: number
  /** run timers that simulate the server (off in Storybook) */
  simulate?: boolean
  update?: UpdateAction | 'none'
  metrics?: 'on' | 'off' | 'admin' | 'profile' | 'none'
  /** artificial latency per request, ms */
  latency?: number
}

function costs (model: string, ctx: number, ttlSec: number, marginMin: number): Costs {
  const p = prices.find(([re]) => re.test(model))
  const [, read, write, out] = p ?? [/./, 0, 0, 0]
  const interval = ttlSec - marginMin * 60
  return {
    readPerM: read,
    writePerM: write,
    outputPerM: out,
    recache: ctx * write / 1e6,
    ping: ctx * read / 1e6 + 40 * write / 1e6 + 6 * out / 1e6,
    pingInterval: interval,
    breakEvenHours: read ? Math.round(Math.max(0, Math.floor(write / read - 1)) * interval / 36) / 100 : 0,
    known: !!p,
  }
}

/** MockBackend holds the fake server state. */
export class MockBackend {
  readonly t0: number
  readonly opts: Required<MockOptions>
  sessions: SessionView[] = []
  pings: PingRecord[] = []
  ledger: LedgerEntry[] = []
  settings: Settings = { maxHours: 36, marginMin: 5, scanSeconds: 5, showMissed: true }
  info: Info
  update: UpdateStatus
  metrics: MetricsStatus
  stopped = false
  private planSeq = 100
  private pingSeq = 0
  private ledgerSeq = 1000
  private listeners = new Set<(e: ServerEvent) => void>()
  private stateListeners = new Set<(s: LinkState) => void>()
  private timers: ReturnType<typeof setTimeout>[] = []
  private down = false

  constructor (opts: MockOptions = {}) {
    this.opts = { now: Date.now(), simulate: true, update: 'apply', metrics: 'on', latency: 150, ...opts }
    this.t0 = this.opts.now
    this.info = {
      name: 'agent-keepalive',
      version: MOCK_VERSION,
      os: 'linux',
      arch: 'amd64',
      profile: 'public',
      installKind: this.opts.update === 'command' ? 'deb' : this.opts.update === 'download' ? 'archive' : 'appimage',
      dataDir: '/home/dev/.local/state/agent-keepalive',
      supervised: false,
      homepage: 'https://github.com/gurupras/agent-keepalive',
      downloadUrl: 'https://github.com/gurupras/agent-keepalive/releases',
    }
    this.update = this.initialUpdate()
    this.metrics = this.initialMetrics()
    this.build()
  }

  now () {
    return this.opts.simulate ? Date.now() : this.t0
  }

  private initialUpdate (): UpdateStatus {
    const a = this.opts.update
    if (a === 'none') return { enabled: true, current: MOCK_VERSION, state: 'idle', progress: 0, lastCheck: iso(this.t0 - 40 * MIN) }
    return {
      enabled: true,
      current: MOCK_VERSION,
      latest: {
        version: MOCK_NEXT_VERSION,
        notes: NOTES,
        page: 'https://github.com/gurupras/agent-keepalive/releases/tag/v' + MOCK_NEXT_VERSION,
        publishedAt: iso(this.t0 - 26 * HOUR),
      },
      state: 'available',
      progress: 0,
      action: a,
      command: a === 'command' ? 'sudo apt update && sudo apt install --only-upgrade agent-keepalive' : undefined,
      lastCheck: iso(this.t0 - 40 * MIN),
    }
  }

  private initialMetrics (): MetricsStatus {
    const m = this.opts.metrics
    if (m === 'none') return { available: false, enabled: false, locked: false, queued: 0 }
    const locked = m === 'admin' || m === 'profile'
    return {
      available: true,
      enabled: m !== 'off',
      locked,
      lockedBy: locked ? m : undefined,
      endpoint: 'https://agent-keepalive.gurupras.me/hub',
      installId: '8d3c6f0e-2b7a-4f5e-9c1d-3a4b5c6d7e8f',
      lastSent: m === 'off' ? undefined : iso(Date.UTC(2026, 9, 27, 0, 4)),
      queued: m === 'off' ? 0 : 1,
    }
  }

  private build () {
    const t0 = this.t0
    this.sessions = fixtures.map((fx, i): SessionView => {
      const ttl = fx.harness === 'claude' ? 3600 : 1800
      const id = `${(0x1a2b3c4d + i * 7919).toString(16)}-4e5f-mock-${String(i).padStart(4, '0')}`
      const project = HOME + fx.project
      const cwd = fx.sub ? `${project}/${fx.sub}` : project
      const c = costs(fx.model, fx.ctx, ttl, this.settings.marginMin)
      const last = t0 - fx.idleMin * MIN
      const s: SessionView = {
        key: `${fx.harness}:${id}`,
        harness: fx.harness,
        id,
        pid: 41000 + i * 37,
        startedAt: iso(t0 - (fx.humanMin ?? 60) * MIN - 2 * HOUR),
        cwd,
        project,
        title: fx.title,
        model: fx.model,
        status: fx.working ? 'working' : 'idle',
        statusSince: iso(t0 - (fx.working ? fx.workingMin ?? 1 : fx.idleMin) * MIN),
        reach: fx.reachWhy ? 'none' : fx.harness === 'claude' ? 'socket' : 'queue',
        reachWhy: fx.reachWhy,
        contextTokens: fx.ctx,
        lastCall: iso(last),
        lastHuman: fx.humanMin === undefined ? '' : iso(t0 - fx.humanMin * MIN),
        cacheTtl: ttl,
        totals: { input: 2400, output: 61_000, cacheRead: fx.ctx * 28, cacheWrite5m: 0, cacheWrite1h: Math.round(fx.ctx * 1.3) },
        costs: c,
        cacheExpiresAt: iso(last + ttl * SEC),
        savedTotal: 0,
        wastedTotal: 0,
      }
      if (fx.plan) {
        const p = fx.plan
        const started = t0 - p.startedMin * MIN
        const hours = p.hours ?? 8
        const plan: Plan = {
          id: ++this.planSeq,
          key: s.key,
          pid: s.pid,
          hours,
          startedAt: iso(started),
          until: iso(started + hours * HOUR),
          state: p.state ?? 'active',
          indefinite: p.indefinite,
          pings: p.pings,
          pingCost: p.pings * c.ping,
          reason: p.reason,
          blocked: p.blocked,
          lastError: p.lastError,
        }
        if (plan.state === 'active' && p.nextPingSec !== undefined) {
          const next = t0 + p.nextPingSec * SEC
          plan.nextPing = iso(next)
          // A plan that has been pinging: the cache was refreshed one interval before the next ping.
          if (!fx.working) {
            s.lastCall = iso(next - c.pingInterval * SEC)
            s.cacheExpiresAt = iso(next - c.pingInterval * SEC + ttl * SEC)
          }
        }
        s.plan = plan
        this.pings.push(...this.pastPings(s, plan, started))
      }
      return s
    })
    this.ledger = ledgerFixtures.map(l => this.ledgerEntry(this.sessions[l.fx]!, t0 - l.agoH * HOUR, l.idleH, l.endedBy, l.pings))
    this.recomputeTotals()
  }

  private pastPings (s: SessionView, plan: Plan, started: number): PingRecord[] {
    const out: PingRecord[] = []
    const step = s.costs.pingInterval * SEC
    for (let n = 0; n < plan.pings; n++) {
      const cold = plan.state === 'paused' && n >= plan.pings - 2
      out.push(this.pingRecord(s, plan.id, started + (n + 1) * step, 'verified', cold))
    }
    if (plan.blocked) {
      out.push({ ...this.pingRecord(s, plan.id, started + 20 * MIN, 'delivered', false), cost: 0, calls: 0 })
    }
    if (plan.indefinite) {
      // Some history variety: a delivery that failed and a queued ping withdrawn.
      out.push({ ...this.pingRecord(s, plan.id, started + 3 * MIN, 'failed', false), error: 'The messaging socket refused the connection.' })
      out.push(this.pingRecord(s, plan.id, started + 2 * MIN, 'withdrawn', false))
    }
    return out
  }

  private pingRecord (s: SessionView, planId: number, at: number, state: PingState, cold: boolean): PingRecord {
    const done = state === 'verified'
    return {
      id: 'p' + (++this.pingSeq).toString(36).padStart(6, '0'),
      planId,
      key: s.key,
      sentAt: iso(at),
      state,
      calls: done ? 1 : 0,
      usage: done
        ? { input: 12, output: 4, cacheRead: cold ? 0 : s.contextTokens, cacheWrite5m: 0, cacheWrite1h: cold ? s.contextTokens : 40 }
        : { input: 0, output: 0, cacheRead: 0, cacheWrite5m: 0, cacheWrite1h: 0 },
      cost: done ? (cold ? s.costs.recache : s.costs.ping) : 0,
      cold,
    }
  }

  private ledgerEntry (s: SessionView, ended: number, idleH: number, endedBy: string, pings: number): LedgerEntry {
    const saved = endedBy === 'returned'
    const pingCost = pings * s.costs.ping
    const avoided = saved ? s.costs.recache : 0
    return {
      id: ++this.ledgerSeq,
      planId: s.plan?.id ?? 50,
      key: s.key,
      harness: s.harness,
      title: s.title,
      project: s.project,
      model: s.model,
      idleFrom: iso(ended - idleH * HOUR),
      endedAt: iso(ended),
      endedBy,
      outcome: saved ? 'saved' : 'wasted',
      pings,
      pingCost,
      avoided,
      net: saved ? avoided - pingCost : -pingCost,
      contextSize: s.contextTokens,
    }
  }

  private totalsFor (entries: LedgerEntry[]): Totals {
    const t: Totals = { saved: 0, wasted: 0, net: 0, pingCost: 0, pings: 0, returns: 0, expired: 0 }
    for (const e of entries) {
      if (e.outcome === 'saved') {
        t.saved += e.net
        t.returns++
      } else {
        t.wasted += -e.net
        if (e.endedBy === 'expired') t.expired++
      }
      t.pings += e.pings
      t.pingCost += e.pingCost
    }
    t.net = t.saved - t.wasted
    return t
  }

  private recomputeTotals () {
    for (const s of this.sessions) {
      const mine = this.ledger.filter(e => e.key === s.key)
      const t = this.totalsFor(mine)
      s.savedTotal = t.saved
      s.wastedTotal = t.wasted
    }
  }

  missed (since: number): Missed {
    const list = this.sessions.filter(s => !s.plan).map(s => {
      const returns = s.costs.known ? 2 : 0
      const expired = s.costs.known ? 1 : 0
      const saved = returns * (s.costs.recache - 4 * s.costs.ping)
      const wasted = expired * 12 * s.costs.ping
      return { key: s.key, title: s.title, project: s.project, model: s.model, saved, wasted, net: saved - wasted, returns, expired, known: s.costs.known }
    })
    const sum = (f: (x: typeof list[number]) => number) => list.reduce((a, x) => a + f(x), 0)
    return {
      since: iso(since),
      hours: this.settings.maxHours,
      saved: sum(x => x.saved),
      wasted: sum(x => x.wasted),
      net: sum(x => x.net),
      returns: sum(x => x.returns),
      expired: sum(x => x.expired),
      sessions: list.sort((a, b) => b.net - a.net),
    }
  }

  preview () {
    const day = new Date(this.now() - 86_400_000).toISOString().slice(0, 10)
    const byOutcome = new Map<string, Record<string, number | string>>()
    for (const e of this.ledger.slice(0, 5)) {
      const k = [e.harness, e.model, e.outcome, e.endedBy].join('|')
      const o = byOutcome.get(k) ?? {
        harness: e.harness,
        model: e.model,
        outcome: e.outcome,
        endedBy: e.endedBy,
        count: 0,
        pings: 0,
        pingCostUsd: 0,
        avoidedUsd: 0,
        netUsd: 0,
        contextTokens: 0,
        idleMinutes: 0,
      }
      const n = (f: string) => o[f] as number
      Object.assign(o, {
        count: n('count') + 1,
        pings: n('pings') + e.pings,
        pingCostUsd: Math.round((n('pingCostUsd') + e.pingCost) * 100) / 100,
        avoidedUsd: Math.round((n('avoidedUsd') + e.avoided) * 100) / 100,
        netUsd: Math.round((n('netUsd') + e.net) * 100) / 100,
        contextTokens: n('contextTokens') + e.contextSize,
        idleMinutes: n('idleMinutes') + Math.round((Date.parse(e.endedAt) - Date.parse(e.idleFrom)) / MIN),
      })
      byOutcome.set(k, o)
    }
    return {
      schema: 1,
      installId: this.metrics.installId,
      day,
      version: this.info.version,
      os: this.info.os,
      arch: this.info.arch,
      profile: this.info.profile,
      installKind: this.info.installKind,
      sessions: { claude: this.sessions.filter(s => s.harness === 'claude').length, codex: this.sessions.filter(s => s.harness === 'codex').length },
      plans: { started: 3, indefinite: 1, hours: 21.5 },
      pings: { sent: 17, verified: 16, cold: 1, failed: 0, costUsd: 0.84 },
      outcomes: [...byOutcome.values()],
    }
  }

  // ---- transport -----------------------------------------------------------

  emit (e: ServerEvent) {
    if (this.down) return
    const copy = JSON.parse(JSON.stringify(e)) as ServerEvent
    for (const l of this.listeners) l(copy)
  }

  private setLink (s: LinkState) {
    for (const l of this.stateListeners) l(s)
  }

  private hello () {
    this.emit({ type: 'hello', data: { version: this.info.version, info: this.info } })
    this.emit({ type: 'sessions', data: this.sessions })
  }

  private find (key: string) {
    const s = this.sessions.find(x => x.key === key)
    if (!s) throw new ApiError('No session with that key.', 404)
    return s
  }

  route (method: string, path: string, body: unknown): unknown {
    if (this.stopped) throw new ApiError('agent-keepalive is not reachable', 0)
    const [p, q = ''] = path.split('?')
    const query = new URLSearchParams(q)
    const m = /^sessions\/([^/]+)(?:\/(plan|ping|pings))?$/.exec(p!)
    const key = m ? decodeURIComponent(m[1]!) : ''
    const now = this.now()
    switch (`${method} ${m ? 'sessions/' + (m[2] ?? '') : p}`) {
      case 'GET info': return this.info
      case 'GET health': return { name: 'agent-keepalive', version: this.info.version, port: 4344 }
      case 'GET sessions': return this.sessions
      case 'GET sessions/': return this.find(key)
      case 'POST sessions/plan': {
        const s = this.find(key)
        if (s.reach === 'none') throw new ApiError(s.reachWhy || 'This session cannot be reached.', 409)
        if (s.plan && (s.plan.state === 'active' || s.plan.state === 'paused')) throw new ApiError('This session already has a plan.', 409)
        const { hours: h, indefinite } = body as { hours?: number, indefinite?: boolean }
        const cap = Math.min(this.settings.maxHours, s.costs.known ? s.costs.breakEvenHours : Infinity)
        const hours = indefinite ? cap : Math.min(h ?? 0, this.settings.maxHours)
        if (!(hours > 0)) throw new ApiError('hours must be positive', 400)
        const plan: Plan = {
          id: ++this.planSeq,
          key,
          pid: s.pid,
          hours,
          startedAt: iso(now),
          until: iso(now + hours * HOUR),
          state: 'active',
          indefinite: indefinite || undefined,
          nextPing: iso(Math.max(now + 15 * SEC, Date.parse(s.lastCall) + s.costs.pingInterval * SEC)),
          pings: 0,
          pingCost: 0,
        }
        if (this.opts.simulate) plan.nextPing = iso(now + 20 * SEC)
        s.plan = plan
        this.emitLater({ type: 'plan', data: plan }, 50)
        return plan
      }
      case 'PATCH sessions/plan': {
        const s = this.find(key)
        const plan = s.plan
        if (!plan || (plan.state !== 'active' && plan.state !== 'paused')) throw new ApiError('This session has no plan.', 404)
        const b = body as { addHours?: number, resume?: boolean }
        if (b.resume) {
          if (plan.state !== 'paused') throw new ApiError('The plan is not paused.', 409)
          Object.assign(plan, { state: 'active', reason: undefined, nextPing: iso(now + 20 * SEC) })
        } else {
          // As the server: Until is clamped to now + maxHours; already there is an error.
          const add = b.addHours ?? 0
          if (!(add > 0)) throw new ApiError('addHours must be positive', 400)
          const limit = now + this.settings.maxHours * HOUR
          const was = Date.parse(plan.until)
          if (was >= limit) throw new ApiError(`The plan already ends ${this.settings.maxHours}h from now, the longest allowed.`, 400)
          const until = Math.min(was + add * HOUR, limit)
          plan.hours += (until - was) / HOUR
          plan.until = iso(until)
        }
        this.emitLater({ type: 'plan', data: plan }, 50)
        return plan
      }
      case 'DELETE sessions/plan': {
        const s = this.find(key)
        if (!s.plan) throw new ApiError('This session has no plan.', 404)
        s.plan = { ...s.plan, state: 'cancelled', endedAt: iso(now), nextPing: undefined, blocked: undefined }
        this.emitLater({ type: 'plan', data: s.plan }, 50)
        return undefined
      }
      case 'POST sessions/ping': {
        const s = this.find(key)
        if (s.reach === 'none') throw new ApiError(s.reachWhy || 'This session cannot be reached.', 409)
        if (s.status === 'working') throw new ApiError('The session is working; it is pinged once it is idle.', 409)
        return this.sendPing(s)
      }
      case 'GET sessions/pings': {
        const limit = Number(query.get('limit') ?? 50)
        return this.pings.filter(x => x.key === key).sort((a, b) => Date.parse(b.sentAt) - Date.parse(a.sentAt)).slice(0, limit)
      }
      case 'GET ledger': {
        const from = Date.parse(query.get('from') ?? '') || 0
        const to = Date.parse(query.get('to') ?? '') || Infinity
        const entries = this.ledger.filter(e => {
          const t = Date.parse(e.endedAt)
          return t >= from && t <= to
        })
        return { entries, totals: this.totalsFor(entries) }
      }
      case 'GET missed': return this.missed(Date.parse(query.get('since') ?? '') || now - 7 * 24 * HOUR)
      case 'GET settings': return this.settings
      case 'PUT settings': {
        const s = body as Settings
        if (!(s.maxHours >= 0.5 && s.maxHours <= 168)) throw new ApiError('maxHours must be between 0.5 and 168', 400)
        if (!(s.marginMin >= 1 && s.marginMin <= 15)) throw new ApiError('marginMin must be between 1 and 15', 400)
        if (!(s.scanSeconds >= 2 && s.scanSeconds <= 60)) throw new ApiError('scanSeconds must be between 2 and 60', 400)
        this.settings = { ...s }
        this.emitLater({ type: 'settings', data: this.settings }, 50)
        return this.settings
      }
      case 'GET metrics': return this.metrics
      case 'PUT metrics': {
        if (this.metrics.locked) throw new ApiError(`Usage reports are locked by the ${this.metrics.lockedBy === 'admin' ? 'administrator' : 'distribution profile'}.`, 403)
        const on = !!(body as { enabled?: boolean }).enabled
        this.metrics = { ...this.metrics, enabled: on, queued: on ? this.metrics.queued : 0 }
        this.emitLater({ type: 'metrics', data: this.metrics }, 50)
        return this.metrics
      }
      case 'GET metrics/preview': return this.preview()
      case 'POST metrics/reset-id': {
        this.metrics = { ...this.metrics, installId: crypto.randomUUID() }
        this.emitLater({ type: 'metrics', data: this.metrics }, 50)
        return this.metrics
      }
      case 'GET update': return this.update
      case 'POST update/check': {
        this.update = { ...this.update, lastCheck: iso(now) }
        return this.update
      }
      case 'POST update/apply': {
        if (this.update.state !== 'available' || this.update.action !== 'apply') throw new ApiError('No update can be applied here.', 409)
        this.update = { ...this.update, state: 'downloading', progress: 0 }
        if (this.opts.simulate) this.runUpdate()
        return this.update
      }
      case 'POST quit': {
        if (this.info.supervised) throw new ApiError('agent-keepalive is supervised; its host stops it.', 409)
        setTimeout(() => this.stop(), 100)
        return undefined
      }
    }
    throw new ApiError(`mock: no route for ${method} ${path}`, 404)
  }

  private emitLater (e: ServerEvent, ms: number) {
    this.timers.push(setTimeout(() => this.emit(e), ms))
  }

  private sendPing (s: SessionView): PingRecord {
    const planId = s.plan?.state === 'active' ? s.plan.id : 0
    const rec = this.pingRecord(s, planId, this.now(), 'sending', false)
    this.pings.push(rec)
    const step = (state: PingState, ms: number, done?: () => void) => {
      this.timers.push(setTimeout(() => {
        rec.state = state
        if (state === 'verified') {
          Object.assign(rec, { calls: 1, cost: s.costs.ping, usage: { input: 12, output: 4, cacheRead: s.contextTokens, cacheWrite5m: 0, cacheWrite1h: 40 } })
        }
        done?.()
        this.emit({ type: 'ping', data: rec })
      }, ms))
    }
    if (this.opts.simulate) {
      step('delivered', 1200)
      step('seen', 2600)
      step('verified', 4200, () => {
        const t = this.now()
        s.lastCall = iso(t)
        s.cacheExpiresAt = iso(t + s.cacheTtl * SEC)
        if (s.plan && s.plan.id === planId) {
          s.plan.pings++
          s.plan.pingCost += s.costs.ping
          s.plan.nextPing = iso(t + s.costs.pingInterval * SEC)
          s.plan.blocked = undefined
          this.emit({ type: 'plan', data: s.plan })
        }
        this.emit({ type: 'sessions', data: this.sessions })
      })
    }
    return rec
  }

  private runUpdate () {
    let progress = 0
    const tick = () => {
      progress = Math.min(1, progress + 0.12 + Math.random() * 0.1)
      this.update = { ...this.update, state: progress < 1 ? 'downloading' : 'installing', progress }
      this.emit({ type: 'update', data: this.update })
      if (progress < 1) this.timers.push(setTimeout(tick, 450))
      else this.timers.push(setTimeout(() => this.restart(), 1200))
    }
    this.timers.push(setTimeout(tick, 300))
  }

  /** restart drops the socket, then comes back as the new version. */
  private restart () {
    this.down = true
    this.setLink('down')
    this.timers.push(setTimeout(() => {
      this.down = false
      this.info = { ...this.info, version: MOCK_NEXT_VERSION }
      this.update = { enabled: true, current: MOCK_NEXT_VERSION, state: 'idle', progress: 0, lastCheck: iso(this.now()) }
      this.setLink('connecting')
      this.timers.push(setTimeout(() => {
        this.setLink('open')
        this.hello()
        this.emit({ type: 'update', data: this.update })
      }, 400))
    }, 3000))
  }

  stop () {
    this.stopped = true
    this.down = true
    for (const t of this.timers) clearTimeout(t)
    this.timers = []
    this.setLink('down')
  }

  /** simulate runs the scheduler: due pings, a return now and then, sessions finishing work. */
  private simulate () {
    const loop = setInterval(() => {
      if (this.stopped) {
        clearInterval(loop)
        return
      }
      const now = this.now()
      for (const s of this.sessions) {
        const p = s.plan
        if (p?.state !== 'active') continue
        if (!p.indefinite && Date.parse(p.until) <= now) {
          s.plan = { ...p, state: 'done', endedAt: iso(now), nextPing: undefined }
          this.emit({ type: 'plan', data: s.plan })
          continue
        }
        const pending = this.pings.some(x => x.key === s.key && (x.state === 'sending' || x.state === 'delivered'))
        if (!p.blocked && !pending && s.status === 'idle' && p.nextPing && Date.parse(p.nextPing) <= now) {
          p.nextPing = undefined
          const rec = this.sendPing(s)
          this.emit({ type: 'ping', data: rec })
          this.emit({ type: 'plan', data: p })
        }
      }
    }, SEC)
    // The working Codex session finishes its turn after a while.
    this.timers.push(setTimeout(() => {
      const s = this.sessions.find(x => x.status === 'working' && x.harness === 'codex')
      if (!s) return
      const t = this.now()
      Object.assign(s, { status: 'idle', statusSince: iso(t), lastCall: iso(t), cacheExpiresAt: iso(t + s.cacheTtl * SEC), contextTokens: s.contextTokens + 9000 })
      this.emit({ type: 'sessions', data: this.sessions })
    }, 45 * SEC))
    // The user comes back to the first kept session: a saved ledger entry.
    this.timers.push(setTimeout(() => {
      const s = this.sessions.find(x => x.plan?.state === 'active' && !x.plan.indefinite && !x.plan.blocked)
      if (!s?.plan) return
      const t = this.now()
      const e = this.ledgerEntry(s, t, (t - Date.parse(s.plan.startedAt)) / HOUR, 'returned', s.plan.pings)
      this.ledger.unshift(e)
      this.recomputeTotals()
      s.plan = { ...s.plan, state: 'done', endedAt: iso(t), nextPing: undefined }
      Object.assign(s, { status: 'working', statusSince: iso(t), lastHuman: iso(t), lastCall: iso(t), cacheExpiresAt: iso(t + s.cacheTtl * SEC) })
      this.emit({ type: 'ledger', data: e })
      this.emit({ type: 'plan', data: s.plan })
      this.emit({ type: 'sessions', data: this.sessions })
    }, 100 * SEC))
  }

  transport (): Transport {
    let started = false
    return {
      request: async <T>(method: string, path: string, body?: unknown): Promise<T> => {
        if (this.opts.latency) await new Promise(resolve => setTimeout(resolve, this.opts.latency))
        const r = this.route(method, path, body)
        // Copy out, as a real response would be.
        return (r === undefined ? r : JSON.parse(JSON.stringify(r))) as T
      },
      subscribe: (onEvent, onState) => {
        this.listeners.add(onEvent)
        this.stateListeners.add(onState)
        onState('connecting')
        if (!this.stopped) {
          this.timers.push(setTimeout(() => {
            onState('open')
            this.hello()
          }, this.opts.simulate ? 300 : 0))
          if (this.opts.simulate && !started) {
            started = true
            this.simulate()
          }
        }
        return () => {
          this.listeners.delete(onEvent)
          this.stateListeners.delete(onState)
        }
      },
    }
  }
}

/** optionsFromURL reads the scenario parameters (see the top of this file). */
export function optionsFromURL (search = location.search): MockOptions {
  const q = new URLSearchParams(search)
  const pick = <T extends string>(name: string, allowed: readonly T[]) => {
    const v = q.get(name) as T | null
    return v && allowed.includes(v) ? v : undefined
  }
  return {
    update: pick('update', ['apply', 'command', 'download', 'none'] as const),
    metrics: pick('metrics', ['on', 'off', 'admin', 'profile', 'none'] as const),
  }
}

export function mockTransport (opts: MockOptions = optionsFromURL()): Transport {
  const clean = Object.fromEntries(Object.entries(opts).filter(([, v]) => v !== undefined)) as MockOptions
  return new MockBackend(clean).transport()
}
