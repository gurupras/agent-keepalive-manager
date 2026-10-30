// The hub API (docs/metrics.md, Hub API). Every URL is relative, so the
// dashboard works under any --base-path.

export interface Summary {
  installs: number
  activeInstalls: number
  saved: number
  wasted: number
  net: number
  pingCost: number
  pings: number
  cold: number
  returns: number
  /** Installs in the range not counted yet: fewer than establishedDays reports. */
  pending: number
  medianNet: number
  /** net without the `trimmed` installs (1%, rounded down) furthest from zero. */
  trimmedNet: number
  trimmed: number
  /** The largest install's share of the summed |net|, 0–1. */
  topShare: number
  establishedDays: number
}

export interface Point {
  day: string
  value: number
}

export interface BreakdownRow {
  key: string
  installs: number
  saved: number
  wasted: number
  net: number
  pings: number
}

/** One arm of an experiment. Costs are per episode; lower is better. */
export interface ExperimentArm {
  arm: 'hold' | 'ping'
  installs: number
  episodes: number
  returned: number
  warm: number
  pings: number
  pingCalls: number
  meanCostUsd: number
  /** Standard error of meanCostUsd. */
  seCostUsd: number
  /** warm / returned. */
  warmRate: number
  pingsPerEpisode: number
  meanQuietMinutes: number
}

/** ping's mean episode cost minus hold's, with its 95% interval. */
export interface ExperimentDiff {
  meanCostUsd: number
  ciLowUsd: number
  ciHighUsd: number
  significant: boolean
}

export interface Experiment {
  experiment: string
  /** hold first, then ping; an arm without episodes is absent. */
  arms: ExperimentArm[]
  /** null while either arm has fewer than 2 episodes. */
  diff: ExperimentDiff | null
}

export type Metric = 'net' | 'saved' | 'wasted' | 'installs' | 'pings'
export type Dimension = 'model' | 'harness' | 'version' | 'os' | 'installKind' | 'profile'

export interface DayRange {
  from: string
  to: string
}

/** Which installs count: those with establishedDays reports, or every one. */
export type Installs = 'established' | 'all'

/** Unauthorized is thrown when the hub wants the dashboard token. */
export class Unauthorized extends Error {
  constructor () {
    super('dashboard token required')
    this.name = 'Unauthorized'
  }
}

async function errorText (res: Response): Promise<string> {
  try {
    const body = await res.json() as { error?: string }
    if (body.error) return body.error
  } catch {
    // not JSON
  }
  return `${res.status} ${res.statusText}`
}

async function get<T> (path: string, params: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`${path}?${new URLSearchParams(params)}`, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (res.status === 401) throw new Unauthorized()
  if (!res.ok) throw new Error(await errorText(res))
  return await res.json() as T
}

export function summary (r: DayRange, installs: Installs, signal?: AbortSignal): Promise<Summary> {
  return get('v1/summary', { from: r.from, to: r.to, installs }, signal)
}

export function series (r: DayRange, installs: Installs, metric: Metric, signal?: AbortSignal): Promise<Point[]> {
  return get('v1/series', { from: r.from, to: r.to, installs, metric }, signal)
}

export function breakdown (r: DayRange, installs: Installs, by: Dimension, signal?: AbortSignal): Promise<BreakdownRow[]> {
  return get('v1/breakdown', { from: r.from, to: r.to, installs, by }, signal)
}

export async function experiments (r: DayRange, installs: Installs, signal?: AbortSignal): Promise<Experiment[]> {
  const body = await get<{ experiments: Experiment[] }>('v1/experiments', { from: r.from, to: r.to, installs }, signal)
  return body.experiments
}

export interface Session {
  mode: 'public' | 'token'
  authenticated: boolean
}

/** session reports the dashboard mode and whether this browser may read. */
export function session (signal?: AbortSignal): Promise<Session> {
  return get('v1/session', {}, signal)
}

/** login trades the dashboard token for a session cookie. */
export async function login (token: string): Promise<void> {
  const res = await fetch('v1/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
  if (res.status === 401) throw new Unauthorized()
  if (!res.ok) throw new Error(await errorText(res))
}

export async function logout (): Promise<void> {
  await fetch('v1/logout', { method: 'POST', credentials: 'same-origin' })
}
