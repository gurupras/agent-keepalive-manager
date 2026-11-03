// The worked example behind the home page's pitch. The prices are the
// defaults in internal/pricing/pricing.go (Claude Opus 5.5) and the timing is
// the scheduler's: a ping every TTL minus the default 5-minute margin, and a
// plan capped at break-even (pricing.BreakEvenPings).

export const example = {
  model: 'Claude Opus 5.5',
  contextTokens: 500_000,
  ttlMin: 60,
  marginMin: 5,
  readPerMTok: 0.2, // cache read
  writePerMTok: 8, // 1-hour cache write: 2x the $4 input price
}

const mtok = example.contextTokens / 1e6

/** One ping: the whole context read from the cache. */
export const pingCost = mtok * example.readPerMTok
/** Coming back cold: the whole context written to the cache again. */
export const rewriteCost = mtok * example.writePerMTok
/** Pings that cost less than one rewrite: floor(write/read - 1). */
export const breakEvenPings = Math.floor(example.writePerMTok / example.readPerMTok - 1)
/** Minutes between pings. */
export const intervalMin = example.ttlMin - example.marginMin
/** How long a plan keeps the cache warm: the last ping plus one TTL. */
export const warmHours = (breakEvenPings * intervalMin + example.ttlMin) / 60

export function money (v: number): string {
  if (v < 1) return `${Math.round(v * 100)}¢`
  return Number.isInteger(Math.round(v * 100) / 100) ? `$${Math.round(v)}` : `$${v.toFixed(2)}`
}
