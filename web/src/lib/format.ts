const ZERO_YEAR = 1970

/** parseTime returns epoch ms, or undefined for empty and zero Go times. */
export function parseTime (t: string | undefined | null): number | undefined {
  if (!t) return undefined
  const ms = Date.parse(t)
  if (Number.isNaN(ms) || new Date(ms).getUTCFullYear() < ZERO_YEAR) return undefined
  return ms
}

export function money (v: number): string {
  const sign = v < 0 ? '-' : ''
  const a = Math.abs(v)
  if (a > 0 && a < 0.01) return sign + '$' + a.toFixed(3)
  return sign + '$' + a.toFixed(2)
}

export function signedMoney (v: number): string {
  return (v > 0 ? '+' : '') + money(v)
}

/** tokens formats a token count: 950, 12k, 1.2M. */
export function tokens (t: number): string {
  if (t >= 1e6) return (t / 1e6).toFixed(1).replace(/\.0$/, '') + 'M'
  if (t >= 1000) return Math.round(t / 1000) + 'k'
  return String(t)
}

/** span formats a duration in ms: "45 s", "12 min", "3 h 5 min", "2 d 4 h". */
export function span (ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s} s`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  if (h < 48) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`
  const d = Math.floor(h / 24)
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`
}

/** countdown formats ms as "m:ss" or "h:mm:ss". */
export function countdown (ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

/** relative formats a time relative to now: "in 5 min", "3 h ago", "just now". */
export function relative (ms: number, now: number): string {
  const d = ms - now
  if (Math.abs(d) < 5000) return 'just now'
  return d > 0 ? `in ${span(d)}` : `${span(-d)} ago`
}

/** clock is a time of day, with the weekday when not today. */
export function clock (ms: number, now: number): string {
  const d = new Date(ms)
  const sameDay = new Date(now).toDateString() === d.toDateString()
  return d.toLocaleString([], sameDay
    ? { hour: '2-digit', minute: '2-digit' }
    : { weekday: 'short', hour: '2-digit', minute: '2-digit' })
}

export function dateTime (ms: number): string {
  return new Date(ms).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export function dateOnly (ms: number): string {
  return new Date(ms).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' })
}

/** hoursLabel formats a plan length: 8 -> "8h", 35.75 -> "35h 45m", 0.5 -> "30m". */
export function hoursLabel (h: number): string {
  const total = Math.round(h * 60)
  const whole = Math.floor(total / 60)
  const min = total % 60
  if (!whole) return `${min}m`
  return min ? `${whole}h ${min}m` : `${whole}h`
}

export function baseName (p: string): string {
  return p.split(/[\\/]/).filter(Boolean).pop() ?? p
}

export function shortModel (m: string): string {
  return (m || 'unknown model').replace(/^claude-/, '').replace(/-\d{8}$/, '')
}

export function percent (v: number): string {
  return Math.round(v * 100) + '%'
}
