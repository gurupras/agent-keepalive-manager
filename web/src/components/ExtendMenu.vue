<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, useTemplateRef } from 'vue'
import AppIcon from './AppIcon.vue'
import { planCap, type Row } from '../lib/derive'
import { hoursLabel } from '../lib/format'
import { extendPlan, store } from '../api/store'

const props = defineProps<{ row: Row }>()

const STEPS = [1, 2, 4, 8] as const
const id = useId()
const open = ref(false)
const busy = ref(false)
const root = useTemplateRef<HTMLElement>('root')

const cap = computed(() => planCap(props.row.s, store.settings))
/**
 * Hours the plan can grow by: the time left after extending must stay within
 * the cap, measured from now (the server clamps Until to now + maxHours; the
 * break-even part of the cap is ours). Rounded down to 15 minutes.
 */
const room = computed(() => {
  const leftH = Math.max(0, (props.row.untilMs ?? 0) / 3_600_000)
  return Math.max(0, Math.floor((cap.value.hours - leftH) * 4 + 1e-9) / 4)
})
const steps = computed(() => {
  const out: number[] = STEPS.filter(h => h <= room.value)
  if (room.value >= 0.25 && !out.includes(room.value)) out.push(room.value)
  return out.sort((a, b) => a - b)
})
const why = computed(() => room.value < 0.25
  ? `This plan already runs to the ${hoursLabel(cap.value.hours)} limit from now. ${cap.value.why}`
  : `Extend up to ${hoursLabel(room.value)}. ${cap.value.why}`)

function onDoc (e: MouseEvent) {
  if (!root.value?.contains(e.target as Node)) close()
}

async function toggle () {
  open.value = !open.value
  if (open.value) {
    document.addEventListener('mousedown', onDoc)
    await nextTick()
    root.value?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
  } else document.removeEventListener('mousedown', onDoc)
}

function close () {
  open.value = false
  document.removeEventListener('mousedown', onDoc)
}

async function pick (h: number) {
  busy.value = true
  await extendPlan(props.row.key, h)
  busy.value = false
  close()
}

function onKey (e: KeyboardEvent) {
  const items = [...(root.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
  const i = items.indexOf(document.activeElement as HTMLElement)
  if (e.key === 'ArrowDown') items[(i + 1) % items.length]?.focus()
  else if (e.key === 'ArrowUp') items[(i - 1 + items.length) % items.length]?.focus()
  else if (e.key === 'Escape') {
    e.stopPropagation()
    close()
    root.value?.querySelector<HTMLElement>('button')?.focus()
  } else return
  e.preventDefault()
}

onBeforeUnmount(close)
</script>

<template>
  <div ref="root" class="relative inline-block" @keydown="onKey">
    <button
      type="button" class="btn-outline" :aria-expanded="open" aria-haspopup="menu" :aria-controls="id"
      :disabled="busy || !steps.length" :title="why"
      @click="toggle"
    >
      <AppIcon :name="busy ? 'lucide:loader-circle' : 'lucide:plus'" :spin="busy" /> Extend
    </button>
    <div v-if="open" :id="id" role="menu" class="absolute right-0 z-30 mt-1 w-56 rounded-lg border border-line bg-surface p-1 shadow-pop">
      <button
        v-for="h in steps" :key="h" type="button" role="menuitem"
        class="flex w-full cursor-pointer items-center rounded-md px-2.5 py-1.5 text-left text-[13px] text-ink-1 outline-none hover:bg-surface-2 focus-visible:bg-surface-2"
        @click="pick(h)"
      >Add {{ hoursLabel(h) }}</button>
      <p class="border-t border-line px-2.5 pt-1.5 pb-1 text-[11px] text-ink-2 mt-1">{{ why }}</p>
    </div>
  </div>
</template>
