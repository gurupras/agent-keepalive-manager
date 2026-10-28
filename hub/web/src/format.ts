const usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 2 })
const usdCompact = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', notation: 'compact', maximumFractionDigits: 1 })
const int = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 })
const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 })
const pct = new Intl.NumberFormat('en-US', { style: 'percent', maximumFractionDigits: 1 })

export function money (v: number): string {
  return Math.abs(v) >= 100_000 ? usdCompact.format(v) : usd.format(v)
}

export function count (v: number): string {
  return Math.abs(v) >= 100_000 ? compact.format(v) : int.format(v)
}

export function percent (v: number): string {
  return pct.format(v)
}

/** axis formats a chart tick: short, and money with a $. */
export function axis (v: number, isMoney: boolean): string {
  const s = compact.format(v)
  if (!isMoney) return s
  return v < 0 ? `-$${s.slice(1)}` : `$${s}`
}

const monthDay = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' })

export function shortDay (day: string): string {
  return monthDay.format(new Date(`${day}T00:00:00Z`))
}
