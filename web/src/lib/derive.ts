import type { LedgerEntry, Plan, SessionView, Settings, Totals } from '../api/types'
import { baseName, hoursLabel, parseTime } from './format'

export const HOUR_PRESETS = [1, 2, 4, 8, 12, 24] as const

/** A session "working" this long without news is treated as stale (scheduler rule 9). */
export const STALE_MS = 2 * 3600_000

/** Row is a SessionView with the time-dependent fields the UI needs. */
export interface Row {
  s: SessionView
  key: string
  title: string
  /** folder relative to the project root, "." for the root itself */
  where: string
  working: boolean
  /** reported working for longer than any turn takes */
  stale: boolean
  /** an active or paused plan (finished plans are not shown on the row) */
  plan?: Plan
  kept: boolean
  paused: boolean
  /** ms until the cache expires; negative once cold */
  leftMs: number
  /** the cache TTL in ms */
  ttlMs: number
  warm: boolean
  /** ms since the user last typed, if known */
  humanAgoMs?: number
  /** ms until the next planned ping, if any */
  nextPingMs?: number
  /** ms until the plan ends (not for indefinite plans) */
  untilMs?: number
}

export function sessionTitle (s: Pick<SessionView, 'title' | 'cwd' | 'id'>): string {
  return s.title?.trim() || baseName(s.cwd) || s.id.slice(0, 8)
}

export function relativeWhere (cwd: string, project: string): string {
  if (!cwd || cwd === project) return '.'
  if (project && cwd.startsWith(project + '/')) return './' + cwd.slice(project.length + 1)
  return cwd
}

/** livePlan is the plan a row shows: active or paused. */
export function livePlan (p: Plan | null | undefined): Plan | undefined {
  return p && (p.state === 'active' || p.state === 'paused') ? p : undefined
}

export function toRow (s: SessionView, now: number): Row {
  const ttlMs = (s.cacheTtl || 3600) * 1000
  const working = s.status === 'working'
  const since = parseTime(s.statusSince)
  const plan = livePlan(s.plan)
  const exp = parseTime(s.cacheExpiresAt)
  const human = parseTime(s.lastHuman)
  const next = plan?.state === 'active' ? parseTime(plan.nextPing) : undefined
  const until = plan && !plan.indefinite ? parseTime(plan.until) : undefined
  const leftMs = working ? ttlMs : exp === undefined ? -1 : exp - now
  return {
    s,
    key: s.key,
    title: sessionTitle(s),
    where: relativeWhere(s.cwd, s.project),
    working,
    stale: working && since !== undefined && now - since > STALE_MS,
    plan,
    kept: plan?.state === 'active',
    paused: plan?.state === 'paused',
    leftMs,
    ttlMs,
    warm: working || leftMs > 0,
    humanAgoMs: human === undefined ? undefined : Math.max(0, now - human),
    nextPingMs: next === undefined ? undefined : next - now,
    untilMs: until === undefined ? undefined : until - now,
  }
}

export interface Cap {
  /** the longest plan offered, in hours */
  hours: number
  /** what set it */
  by: 'settings' | 'breakEven'
  /** a sentence for the UI */
  why: string
}

/**
 * planCap is the longest plan worth offering: the max-hours setting, or the
 * model's break-even (after which pings have cost more than one re-cache),
 * whichever is lower.
 */
export function planCap (s: SessionView, settings: Settings | undefined): Cap {
  const max = settings?.maxHours ?? 36
  const be = s.costs.known && s.costs.breakEvenHours > 0 ? s.costs.breakEvenHours : Infinity
  if (be < max) {
    return {
      hours: be,
      by: 'breakEven',
      why: `Capped at ${hoursLabel(be)}: after that, the pings would cost more than re-caching this context once.`,
    }
  }
  return {
    hours: max,
    by: 'settings',
    why: `Capped at ${hoursLabel(max)} by the "Longest plan" setting.`,
  }
}

/** presets lists the hour options offered: the standard ones that fit, then the cap itself. */
export function presets (cap: number): number[] {
  const out: number[] = HOUR_PRESETS.filter(h => h <= cap)
  if (cap - (out.at(-1) ?? 0) >= 0.25) out.push(cap)
  return out
}

export function pingIntervalSec (s: SessionView, settings: Settings | undefined): number {
  if (s.costs.pingInterval > 0) return s.costs.pingInterval
  return Math.max(60, (s.cacheTtl || 3600) - (settings?.marginMin ?? 5) * 60)
}

/** pingsFor is the most pings an `hours`-long plan can send. */
export function pingsFor (s: SessionView, hours: number, settings: Settings | undefined): number {
  return Math.ceil(hours * 3600 / pingIntervalSec(s, settings))
}

/** quote estimates a plan of `hours`: worst-case ping cost against one re-cache. */
export function quote (s: SessionView, hours: number, settings: Settings | undefined) {
  const pings = pingsFor(s, hours, settings)
  const cost = pings * s.costs.ping
  return { pings, cost, recache: s.costs.recache, saving: s.costs.recache - cost }
}

/** perHour is what pings cost per hour of keeping the session warm. */
export function perHour (s: SessionView, settings: Settings | undefined): number {
  return s.costs.ping * 3600 / pingIntervalSec(s, settings)
}

/** noReachWhy explains why a session cannot be pinged. */
export function noReachWhy (s: SessionView): string {
  return s.reachWhy || (s.harness === 'claude'
    ? 'This Claude Code session has no messaging socket.'
    : 'This Codex session cannot take queued messages.')
}

export interface ProjectGroup {
  id: string
  name: string
  rows: Row[]
  /** sessions with an active or paused plan */
  kept: number
  /** warm sessions nobody keeps warm */
  warm: number
  /** something in it is kept or warm */
  active: boolean
  /** "2 kept warm · 1 warm · 4 sessions" */
  line: string
  /** the idle, unkept session whose cache expires first */
  nextCold?: Row
  /** what re-caching every warm session would cost */
  warmValue: number
  dot: Dot
}

export type Dot = 'kept' | 'warm' | 'cold'

/** sortRows puts kept sessions first, then working, then the soonest to go cold, then cold. */
export function sortRows (a: Row, b: Row): number {
  const rank = (r: Row) => r.kept || r.paused ? 0 : r.working ? 1 : r.warm ? 2 : 3
  return rank(a) - rank(b) || (rank(a) === 2 ? a.leftMs - b.leftMs : 0) || a.title.localeCompare(b.title)
}

/** coldOrder sorts cold sessions most recently used first. */
export function coldOrder (a: Row, b: Row): number {
  return (b.leftMs - a.leftMs) || a.title.localeCompare(b.title)
}

/** isLive: kept, paused or warm, the sessions a project page lists in full. */
export function isLive (r: Row): boolean {
  return r.kept || r.paused || r.warm
}

export function groupProjects (rows: Row[]): ProjectGroup[] {
  const m = new Map<string, Row[]>()
  for (const r of rows) {
    const id = r.s.project || r.s.cwd || '?'
    m.set(id, [...(m.get(id) ?? []), r])
  }
  return [...m].map(([id, list]): ProjectGroup => {
    const kept = list.filter(r => r.kept || r.paused).length
    const warm = list.filter(r => r.warm && !r.kept && !r.paused).length
    const n = list.length
    return {
      id,
      name: baseName(id),
      rows: list.sort(sortRows),
      kept,
      warm,
      active: kept + warm > 0,
      line: [kept && `${kept} kept warm`, warm && `${warm} warm`, `${n} session${n === 1 ? '' : 's'}`].filter(Boolean).join(' · '),
      nextCold: list.filter(r => !r.kept && !r.working && r.leftMs > 0).sort((a, b) => a.leftMs - b.leftMs)[0],
      warmValue: list.filter(r => r.warm).reduce((a, r) => a + r.s.costs.recache, 0),
      dot: kept ? 'kept' : warm ? 'warm' : 'cold',
    }
  // Projects with something kept or warm first, then alphabetical. Not by time
  // left, which changes every second and would reshuffle the list under the pointer.
  }).sort((a, b) =>
    (Number(b.kept > 0) - Number(a.kept > 0)) ||
    (Number(b.active) - Number(a.active)) ||
    a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }) ||
    a.id.localeCompare(b.id))
}

/** sumEntries totals ledger entries the way the server does (store.SumTotals). */
export function sumEntries (es: LedgerEntry[]): Totals {
  const t: Totals = { saved: 0, wasted: 0, net: 0, pingCost: 0, pings: 0, returns: 0, expired: 0 }
  for (const e of es) {
    t.net += e.net
    t.pingCost += e.pingCost
    t.pings += e.pings
    if (e.outcome === 'saved') {
      t.saved += e.net
      t.returns++
    } else {
      t.wasted -= e.net
      t.expired++
    }
  }
  return t
}

/** compareVersions compares dotted versions numerically ("1.10.0" > "1.9.2"); a leading v is ignored. */
export function compareVersions (a: string, b: string): number {
  const parts = (v: string) => v.replace(/^v/, '').split(/[.+-]/).map(x => Number.parseInt(x, 10))
  const pa = parts(a)
  const pb = parts(b)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? 0
    const y = pb[i] ?? 0
    if (Number.isNaN(x) || Number.isNaN(y)) return a.localeCompare(b)
    if (x !== y) return x - y
  }
  return 0
}
