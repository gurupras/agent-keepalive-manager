const usd = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 2 })
const usdCompact = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', notation: 'compact', maximumFractionDigits: 1 })
const int = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 })
const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 })
const pct = new Intl.NumberFormat('en-US', { style: 'percent', maximumFractionDigits: 1 })
const dec = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 })

export function money (v: number): string {
  return Math.abs(v) >= 100_000 ? usdCompact.format(v) : usd.format(v)
}

// usdFine shows sub-dollar amounts, such as one episode's cost, to a
// hundredth of a cent.
const usdFine = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 4 })

export function moneyFine (v: number): string {
  return Math.abs(v) < 1 ? usdFine.format(v) : money(v)
}

export function count (v: number): string {
  return Math.abs(v) >= 100_000 ? compact.format(v) : int.format(v)
}

export function percent (v: number): string {
  return pct.format(v)
}

/** decimal formats a small ratio, such as pings per episode. */
export function decimal (v: number): string {
  return dec.format(v)
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
