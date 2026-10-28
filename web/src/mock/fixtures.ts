// Synthetic sessions for the mock backend and Storybook. Paths, titles and
// models are made up.
import type { Harness, LedgerEntry, PlanState } from '../api/types'

export const HOME = '/home/dev/src/'

export interface PlanFixture {
  state?: PlanState
  hours?: number
  indefinite?: boolean
  /** minutes since the plan started */
  startedMin: number
  /** seconds until the next ping */
  nextPingSec?: number
  pings: number
  reason?: string
  blocked?: string
  lastError?: string
}

export interface Fixture {
  harness: Harness
  project: string
  sub?: string
  title: string
  model: string
  ctx: number
  /** minutes since the last API call */
  idleMin: number
  /** minutes since the user last typed */
  humanMin?: number
  working?: boolean
  /** minutes the session has been reported working */
  workingMin?: number
  reachWhy?: string
  plan?: PlanFixture
}

export const fixtures: Fixture[] = [
  {
    harness: 'claude',
    project: 'agent-keepalive',
    title: 'Ping verification against transcripts',
    model: 'claude-opus-5-5',
    ctx: 412_000,
    idleMin: 12,
    humanMin: 95,
    plan: { hours: 8, startedMin: 95, nextPingSec: 40, pings: 2 },
  },
  {
    harness: 'codex',
    project: 'agent-keepalive',
    sub: 'web',
    title: 'Settings dialog layout',
    model: 'gpt-5.5-codex',
    ctx: 168_000,
    idleMin: 0,
    humanMin: 3,
    working: true,
    workingMin: 2,
  },
  {
    harness: 'claude',
    project: 'agent-keepalive',
    title: 'Ledger idempotency tests',
    model: 'claude-sonnet-5-5',
    ctx: 236_000,
    idleMin: 41,
    humanMin: 41,
  },
  {
    harness: 'claude',
    project: 'terminal-grid',
    title: 'Fix resize flicker on wayland',
    model: 'claude-opus-5-5',
    ctx: 655_000,
    idleMin: 30,
    humanMin: 260,
    plan: { indefinite: true, hours: 17.4, startedMin: 260, nextPingSec: 1500, pings: 4 },
  },
  {
    harness: 'claude',
    project: 'terminal-grid',
    sub: 'docs',
    title: 'Rewrite keybinding docs',
    model: 'claude-haiku-5-5',
    ctx: 88_000,
    idleMin: 75,
    humanMin: 75,
  },
  {
    harness: 'codex',
    project: 'payments-api',
    title: 'Retry budget for webhooks',
    model: 'gpt-5.5-codex',
    ctx: 301_000,
    idleMin: 26,
    humanMin: 140,
    plan: {
      state: 'paused',
      hours: 3.75,
      startedMin: 140,
      pings: 5,
      reason: 'Two pings in a row found the cache already cold, so pinging was paused.',
    },
  },
  {
    harness: 'claude',
    project: 'payments-api',
    title: 'Migrate ledger to decimal amounts',
    model: 'claude-sonnet-5-5',
    ctx: 189_000,
    idleMin: 52,
    humanMin: 52,
    plan: {
      hours: 4,
      startedMin: 52,
      nextPingSec: -20,
      pings: 0,
      blocked: 'An earlier ping was delivered but has not shown up in the transcript yet.',
    },
  },
  {
    harness: 'claude',
    project: 'payments-api',
    sub: 'scripts',
    title: 'One-off backfill script',
    model: 'claude-opus-5-5',
    ctx: 97_000,
    idleMin: 18,
    humanMin: 18,
    reachWhy: 'This Claude Code version has no messaging socket; update Claude Code to 2.1 or later.',
  },
  {
    harness: 'claude',
    project: 'dotfiles',
    title: 'Neovim LSP config',
    model: 'claude-opus-5-5',
    ctx: 140_000,
    idleMin: 0,
    humanMin: 180,
    working: true,
    workingMin: 170,
  },
  {
    harness: 'codex',
    project: 'dotfiles',
    title: 'Shell prompt timing',
    model: 'experimental-model-x',
    ctx: 52_000,
    idleMin: 8,
    humanMin: 8,
  },
]

export interface LedgerFixture {
  fx: number
  /** hours before now that the segment ended */
  agoH: number
  idleH: number
  endedBy: LedgerEntry['endedBy']
  pings: number
}

export const ledgerFixtures: LedgerFixture[] = [
  { fx: 0, agoH: 2, idleH: 1.6, endedBy: 'returned', pings: 2 },
  { fx: 3, agoH: 5, idleH: 3.2, endedBy: 'returned', pings: 4 },
  { fx: 5, agoH: 9, idleH: 2.1, endedBy: 'cold', pings: 3 },
  { fx: 2, agoH: 22, idleH: 6.5, endedBy: 'expired', pings: 7 },
  { fx: 0, agoH: 27, idleH: 0.9, endedBy: 'returned', pings: 1 },
  { fx: 4, agoH: 50, idleH: 1.2, endedBy: 'invalidated', pings: 2 },
  { fx: 3, agoH: 75, idleH: 11, endedBy: 'returned', pings: 12 },
  { fx: 6, agoH: 130, idleH: 2.5, endedBy: 'cancelled', pings: 3 },
  { fx: 1, agoH: 200, idleH: 1, endedBy: 'exited', pings: 1 },
  { fx: 0, agoH: 400, idleH: 4, endedBy: 'returned', pings: 5 },
  { fx: 2, agoH: 900, idleH: 2, endedBy: 'switched', pings: 2 },
]
