// The selected date range lives in the URL hash (#/?days=30 or
// #/?from=2026-10-01&to=2026-10-27), so a view can be bookmarked and shared.

import { computed, ref } from 'vue'
import type { DayRange } from './api.ts'

export const PRESETS = [7, 30, 90] as const
const DEFAULT_DAYS = 30
const DAY_RE = /^\d{4}-\d{2}-\d{2}$/

export type Selection = { days: number } | { from: string, to: string }

/** utcDay formats a date as YYYY-MM-DD in UTC, the hub's day. */
export function utcDay (d: Date): string {
  return d.toISOString().slice(0, 10)
}

export function addDays (day: string, n: number): string {
  const d = new Date(`${day}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + n)
  return utcDay(d)
}

export function parseHash (hash: string): Selection {
  const q = new URLSearchParams(hash.replace(/^#\/?\??/, ''))
  const from = q.get('from') ?? ''
  const to = q.get('to') ?? ''
  if (DAY_RE.test(from) && DAY_RE.test(to) && from <= to) return { from, to }
  const days = Number(q.get('days'))
  if (Number.isInteger(days) && days >= 1 && days <= 1000) return { days }
  return { days: DEFAULT_DAYS }
}

export function toHash (s: Selection): string {
  const q = 'days' in s ? new URLSearchParams({ days: String(s.days) }) : new URLSearchParams({ from: s.from, to: s.to })
  return `#/?${q}`
}

export function resolve (s: Selection, today = utcDay(new Date())): DayRange {
  if ('days' in s) return { from: addDays(today, -(s.days - 1)), to: today }
  return { from: s.from, to: s.to }
}

/** useRange keeps the selection and the URL hash in step. */
export function useRange () {
  const selection = ref<Selection>(parseHash(window.location.hash))
  window.addEventListener('hashchange', () => {
    selection.value = parseHash(window.location.hash)
  })
  const range = computed(() => resolve(selection.value))
  function select (s: Selection) {
    const hash = toHash(s)
    if (window.location.hash !== hash) window.location.hash = hash
    selection.value = s
  }
  return { selection, range, select }
}
