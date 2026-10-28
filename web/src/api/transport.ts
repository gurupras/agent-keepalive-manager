import { eventTypes, type ServerEvent } from './types'

export type LinkState = 'connecting' | 'open' | 'down'

/** Transport moves requests and live events between the UI and a backend. */
export interface Transport {
  request<T>(method: string, path: string, body?: unknown): Promise<T>
  /**
   * subscribe streams events until the returned function is called. It
   * reconnects by itself and reports every change of link state.
   */
  subscribe(onEvent: (e: ServerEvent) => void, onState: (s: LinkState) => void): () => void
}

export class ApiError extends Error {
  constructor (message: string, readonly status: number) {
    super(message)
    this.name = 'ApiError'
  }
}

export const TOKEN_HEADER = 'X-Keepalive-Token'
export const RECONNECT_MIN_MS = 1000
export const RECONNECT_MAX_MS = 30_000

/** readToken returns the API token the server wrote into the page. */
export function readToken (doc: Document = document): string {
  return doc.querySelector<HTMLMetaElement>('meta[name="ka-token"]')?.content ?? ''
}

/**
 * wsURL resolves `api/ws` against the page URL (keeping any path prefix the
 * app is served under) and switches the scheme to ws: or wss:.
 */
export function wsURL (token: string, href: string = location.href): string {
  const u = new URL('api/ws', href)
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  u.search = token ? '?token=' + encodeURIComponent(token) : ''
  u.hash = ''
  return u.toString()
}

/** parseEvent reads one WebSocket frame; unknown or malformed frames are dropped. */
export function parseEvent (raw: unknown): ServerEvent | undefined {
  if (typeof raw !== 'string') return undefined
  let v: unknown
  try {
    v = JSON.parse(raw)
  } catch {
    return undefined
  }
  if (!v || typeof v !== 'object' || !('type' in v) || !('data' in v)) return undefined
  if (!(eventTypes as readonly string[]).includes(String(v.type))) return undefined
  return v as ServerEvent
}

/** nextDelay doubles the reconnect delay, capped at RECONNECT_MAX_MS. */
export function nextDelay (ms: number): number {
  return Math.min(Math.max(ms, RECONNECT_MIN_MS) * 2, RECONNECT_MAX_MS)
}

async function errorMessage (res: Response): Promise<string> {
  const text = (await res.text().catch(() => '')).trim()
  try {
    const j = JSON.parse(text) as { error?: string }
    if (j.error) return j.error
  } catch { /* plain-text error */ }
  if (res.status === 401) return 'The API token was refused. Reload the page from agent-keepalive.'
  return text || res.statusText || `HTTP ${res.status}`
}

/** httpTransport talks to the Go server through relative URLs (./api/...). */
export function httpTransport (base = './api/', token = readToken()): Transport {
  return {
    async request<T>(method: string, path: string, body?: unknown): Promise<T> {
      const headers: Record<string, string> = {}
      if (token) headers[TOKEN_HEADER] = token
      // The server refuses state changes without this header (415), also
      // when they carry no body.
      const mutating = method !== 'GET' && method !== 'HEAD'
      if (mutating || body !== undefined) headers['Content-Type'] = 'application/json'
      let res: Response
      try {
        res = await fetch(base + path, {
          method,
          headers,
          body: body === undefined ? undefined : JSON.stringify(body),
          cache: 'no-store',
          credentials: 'same-origin',
        })
      } catch {
        throw new ApiError('agent-keepalive is not reachable', 0)
      }
      if (!res.ok) throw new ApiError(await errorMessage(res), res.status)
      const text = await res.text()
      return (text ? JSON.parse(text) : undefined) as T
    },

    subscribe (onEvent, onState) {
      let ws: WebSocket | undefined
      let timer: ReturnType<typeof setTimeout> | undefined
      let delay = RECONNECT_MIN_MS
      let stopped = false

      const retry = () => {
        if (stopped) return
        onState('down')
        clearTimeout(timer)
        timer = setTimeout(connect, delay)
        delay = nextDelay(delay)
      }

      function connect () {
        if (stopped) return
        onState('connecting')
        try {
          ws = new WebSocket(wsURL(token))
        } catch {
          retry()
          return
        }
        // The link counts as open once the hello frame arrives: an upgrade
        // the server refused (a wrong token answers 401) never gets one.
        let greeted = false
        ws.onmessage = m => {
          const e = parseEvent(m.data)
          if (!e) return
          if (!greeted && e.type === 'hello') {
            greeted = true
            delay = RECONNECT_MIN_MS
            onState('open')
          }
          onEvent(e)
        }
        // onerror is always followed by onclose; reconnect from there only.
        ws.onclose = () => {
          ws = undefined
          retry()
        }
      }

      // Reconnect at once when the page becomes visible or the network
      // returns, instead of waiting out a long backoff.
      const wake = () => {
        if (stopped || ws || document.visibilityState === 'hidden') return
        delay = RECONNECT_MIN_MS
        clearTimeout(timer)
        connect()
      }
      window.addEventListener('online', wake)
      document.addEventListener('visibilitychange', wake)

      connect()
      return () => {
        stopped = true
        clearTimeout(timer)
        window.removeEventListener('online', wake)
        document.removeEventListener('visibilitychange', wake)
        if (ws) {
          ws.onclose = null
          ws.close()
        }
      }
    },
  }
}
