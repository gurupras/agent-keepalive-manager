import { computed, reactive } from 'vue'

// The worked example behind the home page's pitch. Prices are the defaults in
// internal/pricing/pricing.go; the timing is the scheduler's: a ping every
// TTL minus the default 5-minute margin, a plan capped at break-even
// (pricing.BreakEvenPings), and a ping priced as pricing.Costs prices it.

export interface Model { id: string, name: string, input: number, output: number, read: number }
export interface Harness {
  id: 'claude' | 'codex'
  name: string
  ttlMin: number
  cacheLabel: string
  /** Cache-write price per M tokens. */
  write: (m: Model) => number
  models: Model[]
}

export const harnesses: Harness[] = [
  {
    id: 'claude',
    name: 'Claude Code',
    ttlMin: 60,
    cacheLabel: '1-hour cache',
    write: m => m.input * 2, // the 1-hour write is 2x input
    models: [
      { id: 'opus-5-5', name: 'Opus 5.5', input: 4, output: 20, read: 0.2 },
      { id: 'sonnet-5-5', name: 'Sonnet 5.5', input: 2, output: 10, read: 0.2 },
      { id: 'haiku-5-5', name: 'Haiku 5.5', input: 0.1, output: 0.5, read: 0.01 },
    ],
  },
  {
    id: 'codex',
    name: 'Codex CLI',
    ttlMin: 30,
    cacheLabel: '30-minute cache',
    write: m => m.input, // OpenAI writes carry no surcharge
    models: [
      { id: 'gpt-6.1-sol', name: 'GPT-6.1 Sol', input: 2.2, output: 11, read: 0.05 },
      { id: 'gpt-6-astra', name: 'GPT-6 Astra', input: 11, output: 55, read: 1 },
      { id: 'gpt-5.6-terra', name: 'GPT-5.6 Terra', input: 2.2, output: 13.2, read: 0.2 },
      { id: 'gpt-6-luna', name: 'GPT-6 Luna', input: 0.11, output: 0.55, read: 0.01 },
    ],
  },
]

export const contextTokens = 500_000
const marginMin = 5
const pingOutputTokens = 5 // pricing.PingOutputTokens

/** The picker's choice, shared by every chart and heading on the page. */
export const selection = reactive({ harness: 'claude' as Harness['id'], model: 'opus-5-5' })

export function pick (h: Harness['id'], model?: string) {
  selection.harness = h
  selection.model = model ?? harnesses.find(x => x.id === h)!.models[0].id
}

export const scenario = computed(() => {
  const h = harnesses.find(x => x.id === selection.harness)!
  const m = h.models.find(x => x.id === selection.model) ?? h.models[0]
  const write = h.write(m)
  const mtok = contextTokens / 1e6
  const breakEvenPings = Math.floor(write / m.read - 1 + 1e-9)
  const intervalMin = h.ttlMin - marginMin
  return {
    harness: h,
    model: m,
    write,
    /** One ping: the whole context read from the cache, plus a few output tokens. */
    pingCost: mtok * m.read + (pingOutputTokens * m.output) / 1e6,
    /** Coming back cold: the whole context written to the cache again. */
    rewriteCost: mtok * write,
    /** How many cache reads one rewrite costs. */
    ratio: write / m.read,
    breakEvenPings,
    intervalMin,
    ttlH: h.ttlMin / 60,
    /** How long pings stay cheaper than a rebuild: the last ping of the plan (pricing.Costs BreakEvenHours). */
    breakEvenHours: (breakEvenPings * intervalMin) / 60,
  }
})

export function money (v: number): string {
  if (v < 0.995) return `${+(v * 100).toFixed(v < 0.1 ? 1 : 0)}¢`
  return Number.isInteger(Math.round(v * 100) / 100) ? `$${Math.round(v)}` : `$${v.toFixed(2)}`
}

export function duration (h: number): string {
  if (h < 1) return `${Math.round(h * 60)} minutes`
  const whole = Math.floor(h)
  return `${whole} hour${whole === 1 ? '' : 's'}`
}
