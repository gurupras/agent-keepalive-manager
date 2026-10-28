import { computed } from 'vue'
import { persisted } from './persisted'
import { store } from '../api/store'

/** Versions the user dismissed with "Later" (remembered per browser). */
const dismissed = persisted<string[]>('update-dismissed', [])

export function dismissVersion (v: string) {
  if (!dismissed.value.includes(v)) dismissed.value = [...dismissed.value.slice(-9), v]
}

export function undismissVersion (v: string) {
  dismissed.value = dismissed.value.filter(x => x !== v)
}

/** showUpdateToast: an update is on offer (or being installed) and not dismissed. */
export const showUpdateToast = computed(() => {
  const u = store.update
  if (!u?.enabled || store.quit) return false
  if (store.updatePhase !== 'idle') return true
  if (['downloading', 'ready', 'installing'].includes(u.state)) return true
  const v = u.latest?.version
  if (!v) return false
  if (u.state === 'error') return !dismissed.value.includes(v)
  return u.state === 'available' && !dismissed.value.includes(v)
})
