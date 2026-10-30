// JSON shapes of the local API (docs/api.md). Field names mirror the json
// tags in internal/model and internal/apitypes. Times are RFC 3339 strings
// (a zero time is omitted), durations are seconds, money is USD.

export type Harness = 'claude' | 'codex'
export type Status = 'idle' | 'working'
export type Reach = 'socket' | 'queue' | 'none'

export interface Usage {
  input: number
  output: number
  cacheRead: number
  cacheWrite5m: number
  cacheWrite1h: number
}

export interface Costs {
  readPerM: number
  writePerM: number
  outputPerM: number
  /** contextTokens at the cache-write price: what a cold return costs */
  recache: number
  /** one keepalive ping */
  ping: number
  /** seconds between pings (TTL minus the safety margin) */
  pingInterval: number
  /** hours of pings that cost as much as one re-cache */
  breakEvenHours: number
  /** the model is in the pricing table */
  known: boolean
}

export type PlanState = 'active' | 'paused' | 'done' | 'cancelled' | 'ended' | 'failed'

export interface Plan {
  id: number
  key: string
  pid: number
  hours: number
  startedAt: string
  until: string
  state: PlanState
  indefinite?: boolean
  endedAt?: string
  nextPing?: string
  pings: number
  pingCost: number
  /** why the plan is paused, failed or ended */
  reason?: string
  lastError?: string
  /** set while the plan wants to ping but cannot */
  blocked?: string
}

export interface SessionView {
  key: string
  harness: Harness
  id: string
  pid: number
  startedAt: string
  cwd: string
  project: string
  title: string
  /** the harness's own recap of the session (Claude Code's away summary) */
  summary?: string
  model: string
  status: Status
  statusSince?: string
  /** the harness's own word for the state (Claude Code: idle, busy, shell, waiting) */
  harnessStatus?: string
  reach: Reach
  reachWhy?: string
  contextTokens: number
  lastCall: string
  lastHuman: string
  /** seconds */
  cacheTtl: number
  totals: Usage
  costs: Costs
  cacheExpiresAt: string
  plan?: Plan | null
  savedTotal: number
  wastedTotal: number
}

export type PingState = 'sending' | 'delivered' | 'seen' | 'verified' | 'failed' | 'withdrawn' | 'lost'

export interface PingRecord {
  id: string
  planId: number
  key: string
  sentAt: string
  state: PingState
  calls: number
  usage: Usage
  cost: number
  /** the reply re-wrote the context: the cache had already expired */
  cold: boolean
  error?: string
}

export type Outcome = 'saved' | 'wasted'
export type EndedBy = 'returned' | 'expired' | 'cancelled' | 'exited' | 'switched' | 'cold' | 'invalidated'

export interface LedgerEntry {
  id: number
  planId: number
  key: string
  harness: Harness
  title: string
  project: string
  model: string
  idleFrom: string
  endedAt: string
  endedBy: EndedBy | string
  outcome: Outcome
  pings: number
  pingCost: number
  avoided: number
  net: number
  contextSize: number
}

export interface Totals {
  saved: number
  wasted: number
  net: number
  pingCost: number
  pings: number
  returns: number
  expired: number
}

export interface LedgerResponse {
  entries: LedgerEntry[] | null
  totals: Totals
}

export interface MissedSession {
  key: string
  title: string
  project: string
  model: string
  saved: number
  wasted: number
  net: number
  returns: number
  expired: number
  known: boolean
}

export interface Missed {
  since: string
  hours: number
  saved: number
  wasted: number
  net: number
  returns: number
  expired: number
  sessions: MissedSession[] | null
}

export interface Settings {
  maxHours: number
  marginMin: number
  scanSeconds: number
  showMissed: boolean
}

export type InstallKind =
  | 'deb' | 'rpm' | 'arch' | 'brew' | 'appimage'
  | 'windows-setup' | 'windows-portable' | 'macos-pkg' | 'archive' | 'dev'

export interface Info {
  name: string
  version: string
  os: string
  arch: string
  profile: string
  installKind: InstallKind | string
  dataDir: string
  supervised: boolean
  homepage?: string
  downloadUrl?: string
}

export interface Health {
  name: string
  version: string
  port: number
}

export interface MetricsStatus {
  /** false: the build has no hub; the feature is hidden */
  available: boolean
  enabled: boolean
  locked: boolean
  lockedBy?: 'admin' | 'profile' | string
  endpoint?: string
  installId?: string
  lastSent?: string
  lastError?: string
  queued: number
}

export type UpdateState = 'idle' | 'checking' | 'available' | 'downloading' | 'ready' | 'installing' | 'error'
export type UpdateAction = 'apply' | 'command' | 'download' | 'restart'

export interface Latest {
  version: string
  /** markdown */
  notes?: string
  page?: string
  publishedAt?: string
}

export interface UpdateStatus {
  /** false: no feed, --no-update, or supervised; the UI shows nothing */
  enabled: boolean
  current: string
  latest?: Latest | null
  state: UpdateState
  /** 0..1 while downloading */
  progress: number
  action?: UpdateAction
  command?: string
  /** with action 'restart': the newer version installed in place of the running one */
  installed?: string
  error?: string
  lastCheck?: string
}

export interface Hello {
  version: string
  info: Info
}

/** One WebSocket frame (model.Event). */
export type ServerEvent =
  | { type: 'hello', data: Hello }
  | { type: 'sessions', data: SessionView[] | null }
  | { type: 'plan', data: Plan }
  | { type: 'ping', data: PingRecord }
  | { type: 'ledger', data: LedgerEntry }
  | { type: 'settings', data: Settings }
  | { type: 'update', data: UpdateStatus }
  | { type: 'metrics', data: MetricsStatus }

export type EventType = ServerEvent['type']

export const eventTypes: readonly EventType[] = ['hello', 'sessions', 'plan', 'ping', 'ledger', 'settings', 'update', 'metrics']
