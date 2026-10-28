import { ref, watch, type Ref } from 'vue'

const PREFIX = 'agent-keepalive:'

/** readStored returns the JSON value under key, or undefined (missing, corrupt, or no storage). */
export function readStored<T> (key: string): T | undefined {
  try {
    const raw = localStorage.getItem(PREFIX + key)
    return raw === null ? undefined : JSON.parse(raw) as T
  } catch {
    return undefined
  }
}

/** writeStored saves value under key; storage errors (privacy mode, quota) are ignored. */
export function writeStored (key: string, value: unknown) {
  try {
    if (value === undefined) localStorage.removeItem(PREFIX + key)
    else localStorage.setItem(PREFIX + key, JSON.stringify(value))
  } catch { /* storage unavailable */ }
}

/** persisted is a ref backed by localStorage (JSON). */
export function persisted<T> (key: string, initial: T): Ref<T> {
  const r = ref(readStored<T>(key) ?? initial) as Ref<T>
  watch(r, v => writeStored(key, v), { deep: true })
  return r
}
