import { computed, ref } from 'vue'
import { noReachWhy, planCap, type Row } from '../lib/derive'
import { hoursLabel } from '../lib/format'
import { cancelPlan, extendPlan, pingNow, resumePlan, store } from '../api/store'

const STEPS = [1, 2, 4, 8] as const

export type PlanAction = 'stop' | 'resume' | 'ping' | 'extend'

/**
 * usePlanActions holds the controls of a session's plan, shared by the
 * drawer's buttons and the session list's menu.
 */
export function usePlanActions (row: () => Row) {
  const busy = ref<'' | PlanAction>('')

  const canPing = computed(() => row().s.reach !== 'none' && !row().working)
  const pingWhy = computed(() => row().s.reach === 'none'
    ? noReachWhy(row().s)
    : row().working ? 'The session is working; it is pinged once idle.' : 'Send one keepalive ping now')

  const cap = computed(() => planCap(row().s, store.settings))
  /**
   * Hours the plan can grow by: the time left after extending must stay
   * within the cap, measured from now (the server clamps Until to now +
   * maxHours; the break-even part of the cap is ours). Rounded down to 15
   * minutes.
   */
  const room = computed(() => {
    const leftH = Math.max(0, (row().untilMs ?? 0) / 3_600_000)
    return Math.max(0, Math.floor((cap.value.hours - leftH) * 4 + 1e-9) / 4)
  })
  const steps = computed(() => {
    const out: number[] = STEPS.filter(h => h <= room.value)
    if (room.value >= 0.25 && !out.includes(room.value)) out.push(room.value)
    return out.sort((a, b) => a - b)
  })
  const extendWhy = computed(() => room.value < 0.25
    ? `This plan already runs to the ${hoursLabel(cap.value.hours)} limit from now. ${cap.value.why}`
    : `Extend up to ${hoursLabel(room.value)}. ${cap.value.why}`)

  async function run (what: PlanAction, hours = 0) {
    busy.value = what
    const key = row().key
    if (what === 'stop') await cancelPlan(key)
    else if (what === 'resume') await resumePlan(key)
    else if (what === 'extend') await extendPlan(key, hours)
    else await pingNow(key)
    busy.value = ''
  }

  return { busy, run, canPing, pingWhy, steps, extendWhy }
}
