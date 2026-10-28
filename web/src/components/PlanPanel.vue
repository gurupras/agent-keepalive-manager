<script setup lang="ts">
import { computed, onMounted, ref, useId, useTemplateRef } from 'vue'
import AppIcon from './AppIcon.vue'
import { perHour, planCap, presets, quote, type Row } from '../lib/derive'
import { hoursLabel, money } from '../lib/format'
import { keepIndefinitely, startPlan, store } from '../api/store'

const props = defineProps<{ row: Row }>()
const emit = defineEmits<{ done: [], cancel: [] }>()

const DEFAULT_HOURS = 4
const id = useId()

type Choice = number | 'custom' | 'indefinite'

const cap = computed(() => planCap(props.row.s, store.settings))
const options = computed(() => presets(cap.value.hours))
const choice = ref<Choice>(options.value.includes(DEFAULT_HOURS) ? DEFAULT_HOURS : options.value.at(-1) ?? 'custom')
const custom = ref<number | ''>(Math.min(6, Math.floor(cap.value.hours * 4) / 4) || '')
const busy = ref(false)
const chips = useTemplateRef<HTMLElement>('chips')

const hours = computed(() => {
  if (choice.value === 'indefinite') return cap.value.hours
  if (choice.value === 'custom') return typeof custom.value === 'number' ? custom.value : 0
  return choice.value
})

const customError = computed(() => {
  if (choice.value !== 'custom') return ''
  const h = hours.value
  if (!(h > 0)) return 'Enter a number of hours.'
  if (h < 0.25) return 'At least 15 minutes.'
  if (h > cap.value.hours + 1e-9) return `At most ${hoursLabel(cap.value.hours)}.`
  return ''
})

const q = computed(() => quote(props.row.s, hours.value, store.settings))
const hourly = computed(() => perHour(props.row.s, store.settings))
const unreachable = computed(() => props.row.s.reach === 'none')

async function confirm () {
  if (customError.value || busy.value || unreachable.value) return
  busy.value = true
  const ok = choice.value === 'indefinite'
    ? await keepIndefinitely(props.row.key)
    : await startPlan(props.row.key, hours.value)
  busy.value = false
  if (ok) emit('done')
}

const all = computed<Choice[]>(() => [...options.value, 'custom', 'indefinite'])

// Arrow keys move through the choices, as in any radio group.
function onKey (e: KeyboardEvent) {
  const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
  if (!step) return
  e.preventDefault()
  const i = all.value.indexOf(choice.value)
  choice.value = all.value[(i + step + all.value.length) % all.value.length]!
  requestAnimationFrame(() => chips.value?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus())
}

const chipCls = (on: boolean, tone = 'accent') => on
  ? tone === 'warm' ? 'border-warm bg-warm-soft text-warm' : 'border-accent bg-accent-soft text-accent-ink'
  : 'border-line text-ink-1 hover:border-ink-3'

onMounted(() => chips.value?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus())
</script>

<template>
  <form class="w-full" @submit.prevent="confirm" @keydown.esc.stop="emit('cancel')">
    <div :id="id + '-l'" class="label">Keep warm for</div>
    <div ref="chips" role="radiogroup" :aria-labelledby="id + '-l'" class="mt-1.5 flex flex-wrap gap-1.5" @keydown="onKey">
      <button
        v-for="h in options" :key="h" type="button" role="radio"
        :aria-checked="choice === h" :tabindex="choice === h ? 0 : -1"
        class="cursor-pointer rounded-lg border px-2.5 py-1 text-[13px] font-medium tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-accent"
        :class="chipCls(choice === h)"
        @click="choice = h"
      >{{ hoursLabel(h) }}</button>
      <button
        type="button" role="radio" :aria-checked="choice === 'custom'" :tabindex="choice === 'custom' ? 0 : -1"
        class="cursor-pointer rounded-lg border px-2.5 py-1 text-[13px] font-medium outline-none focus-visible:ring-2 focus-visible:ring-accent"
        :class="chipCls(choice === 'custom')"
        @click="choice = 'custom'"
      >Custom</button>
      <button
        type="button" role="radio" :aria-checked="choice === 'indefinite'" :tabindex="choice === 'indefinite' ? 0 : -1"
        class="inline-flex cursor-pointer items-center gap-1 rounded-lg border px-2.5 py-1 text-[13px] font-medium outline-none focus-visible:ring-2 focus-visible:ring-warm"
        :class="chipCls(choice === 'indefinite', 'warm')"
        @click="choice = 'indefinite'"
      ><AppIcon name="lucide:infinity" />Until I stop it</button>
    </div>

    <div v-if="choice === 'custom'" class="mt-2.5 flex items-center gap-2">
      <label :for="id + '-h'" class="text-[13px] text-ink-1">Hours</label>
      <input
        :id="id + '-h'" v-model.number="custom" type="number" inputmode="decimal" min="0.25" :max="cap.hours" step="0.25"
        class="input w-24 tabular-nums" :aria-invalid="!!customError" :aria-describedby="id + '-e ' + id + '-cap'"
      />
      <span :id="id + '-e'" class="text-xs text-danger" role="alert">{{ customError }}</span>
    </div>

    <p :id="id + '-cap'" class="mt-2 flex gap-1.5 text-xs text-ink-2">
      <AppIcon name="lucide:info" class="mt-px" />
      <span>{{ cap.why }}<template v-if="cap.by === 'breakEven'"> Change the longest plan in Settings.</template></span>
    </p>

    <template v-if="choice === 'indefinite'">
      <p class="mt-3 rounded-lg bg-warm-soft px-3 py-2 text-[13px] text-ink-1">
        Pings run whenever you step away, until you stop them. Each absence is kept warm for up to
        <strong class="text-ink">{{ hoursLabel(cap.hours) }}</strong>; only a longer one ends the plan.
      </p>
      <dl class="mt-3 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-[13px]">
        <dt class="text-ink-2">Pings while you're away</dt>
        <dd class="text-right text-ink tabular-nums">{{ money(hourly) }}/h</dd>
        <dt class="text-ink-2">Re-cache avoided each time you return</dt>
        <dd class="text-right text-ink tabular-nums">{{ money(row.s.costs.recache) }}</dd>
        <dt class="text-ink-2">Lost if you're away over {{ hoursLabel(cap.hours) }}</dt>
        <dd class="text-right font-medium text-danger tabular-nums">{{ money(q.cost) }}</dd>
      </dl>
    </template>
    <dl v-else class="mt-3 grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-[13px]">
      <dt class="text-ink-2">Pings, if you're away the whole {{ hoursLabel(hours || 0) }}</dt>
      <dd class="text-right text-ink tabular-nums">{{ q.pings }} × {{ money(row.s.costs.ping) }} = {{ money(q.cost) }}</dd>
      <dt class="text-ink-2">Re-cache without keepalive</dt>
      <dd class="text-right text-ink tabular-nums">{{ money(q.recache) }}</dd>
      <dt class="text-ink-2">Worst-case saving if you return in time</dt>
      <dd class="text-right font-medium tabular-nums" :class="q.saving >= 0 ? 'text-ok' : 'text-danger'">{{ money(q.saving) }}</dd>
    </dl>
    <p v-if="!row.s.costs.known" class="mt-2 flex gap-1.5 text-xs text-warn">
      <AppIcon name="lucide:triangle-alert" class="mt-px" />
      No price is known for {{ row.s.model || 'this model' }}: costs show as $0 and there is no break-even cap.
    </p>
    <p v-if="unreachable" class="mt-2 text-xs text-danger">{{ row.s.reachWhy || 'This session cannot be reached.' }}</p>

    <div class="mt-3 flex flex-wrap justify-end gap-2">
      <button type="button" class="btn-ghost" @click="emit('cancel')">Cancel</button>
      <button type="submit" class="btn-primary" :class="choice === 'indefinite' ? 'bg-warm' : ''" :disabled="busy || !!customError || unreachable">
        <AppIcon :name="busy ? 'lucide:loader-circle' : choice === 'indefinite' ? 'lucide:infinity' : 'lucide:flame'" :spin="busy" />
        {{ choice === 'indefinite' ? 'Keep warm until stopped' : `Keep warm ${hoursLabel(hours || 0)}` }}
      </button>
    </div>
  </form>
</template>
