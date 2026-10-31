<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import ExtendMenu from './ExtendMenu.vue'
import PlanMenu from './PlanMenu.vue'
import SlotButtons from './SlotButtons.vue'
import type { Row } from '../lib/derive'
import { clock, countdown, hoursLabel, money, parseTime, span } from '../lib/format'
import { now } from '../composables/now'
import { usePlanActions } from '../composables/planActions'

// The state of a session's active or paused plan, with its controls.
// `compact` is the one-line form used in the session list.
const props = defineProps<{ row: Row, compact?: boolean }>()

const { busy, run, canPing, pingWhy } = usePlanActions(() => props.row)
const p = computed(() => props.row.plan!)

const headline = computed(() => {
  const plan = p.value
  if (plan.state === 'paused') return 'Paused'
  if (plan.indefinite) return 'Kept warm until you stop it'
  const until = parseTime(plan.until)
  return until ? `Kept warm until ${clock(until, now.value)}` : 'Kept warm'
})

const next = computed(() => {
  const r = props.row
  if (p.value.state !== 'active') return ''
  if (r.working) return 'Next ping once the session is idle'
  if (r.nextPingMs === undefined) return ''
  return r.nextPingMs > 0 ? `Next ping in ${countdown(r.nextPingMs)}` : 'Ping due now'
})

const left = computed(() => props.row.untilMs !== undefined && props.row.untilMs > 0 ? `${span(props.row.untilMs)} left` : '')
</script>

<template>
  <div v-if="row.plan" class="min-w-0">
    <!-- compact: one or two lines in the session list -->
    <div v-if="compact" class="flex items-center gap-x-3 @2xl:justify-end">
      <div class="min-w-0 flex-1 text-[13px] leading-tight @2xl:text-right">
        <div class="flex items-center gap-1 font-medium @2xl:justify-end" :class="p.state === 'paused' ? 'text-warn' : 'text-warm'">
          <AppIcon :name="p.state === 'paused' ? 'lucide:pause' : p.indefinite ? 'lucide:infinity' : 'lucide:flame'" />
          <span class="truncate">{{ headline }}</span>
        </div>
        <div v-if="p.blocked" class="flex items-center gap-1 text-xs text-warn @2xl:justify-end" :title="p.blocked">
          <AppIcon name="lucide:hourglass" /><span class="truncate">Blocked: {{ p.blocked }}</span>
        </div>
        <div v-else-if="p.state === 'paused'" class="truncate text-xs text-ink-2" :title="p.reason">{{ p.reason || 'Pinging is paused.' }}</div>
        <div v-else class="text-xs text-ink-2 tabular-nums">{{ next || left }}<template v-if="next && left"> · {{ left }}</template></div>
      </div>
      <div class="flex shrink-0 items-center gap-1">
        <button v-if="p.state === 'paused'" type="button" class="btn-soft" :disabled="!!busy" @click="run('resume')">
          <AppIcon :name="busy === 'resume' ? 'lucide:loader-circle' : 'lucide:play'" :spin="busy === 'resume'" /> Resume
        </button>
        <PlanMenu :row="row" />
      </div>
    </div>

    <!-- full: the drawer -->
    <div v-else class="rounded-ka border p-3.5" :class="p.state === 'paused' ? 'border-warn/40 bg-warn-soft' : 'border-warm/30 bg-warm-soft'">
      <div class="flex items-center gap-2 font-semibold" :class="p.state === 'paused' ? 'text-warn' : 'text-warm'">
        <AppIcon :name="p.state === 'paused' ? 'lucide:pause' : p.indefinite ? 'lucide:infinity' : 'lucide:flame'" />
        {{ headline }}
      </div>
      <p v-if="p.state === 'paused'" class="mt-1.5 text-[13px] text-ink-1">{{ p.reason || 'Pinging is paused.' }} Resume to try again.</p>
      <p v-else-if="p.indefinite" class="mt-1.5 text-[13px] text-ink-1">
        Re-arms each time you come back; ends only after an absence longer than {{ hoursLabel(p.hours) }}.
      </p>
      <p v-if="p.blocked" class="mt-2 flex gap-1.5 rounded-md bg-surface px-2.5 py-1.5 text-[13px] text-warn">
        <AppIcon name="lucide:hourglass" class="mt-0.5" /><span><strong>Blocked:</strong> {{ p.blocked }}</span>
      </p>
      <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
        <template v-if="next"><dt class="text-ink-2">Next ping</dt><dd class="text-right text-ink tabular-nums">{{ next.replace(/^Next ping /, '') }}</dd></template>
        <template v-if="!p.indefinite && left"><dt class="text-ink-2">Remaining</dt><dd class="text-right text-ink tabular-nums">{{ left }} of {{ hoursLabel(p.hours) }}</dd></template>
        <dt class="text-ink-2">Pings sent</dt><dd class="text-right text-ink tabular-nums">{{ p.pings }}</dd>
        <dt class="text-ink-2">Spent on pings</dt><dd class="text-right text-ink tabular-nums">{{ money(p.pingCost) }}</dd>
        <template v-if="parseTime(p.startedAt)"><dt class="text-ink-2">Started</dt><dd class="text-right text-ink tabular-nums">{{ clock(parseTime(p.startedAt)!, now) }}</dd></template>
      </dl>
      <p v-if="p.lastError" class="mt-2 text-[13px] text-danger">Last error: {{ p.lastError }}</p>
      <div class="mt-3 flex flex-wrap gap-2">
        <button v-if="p.state === 'paused'" type="button" class="btn-primary" :disabled="!!busy" @click="run('resume')">
          <AppIcon :name="busy === 'resume' ? 'lucide:loader-circle' : 'lucide:play'" :spin="busy === 'resume'" /> Resume
        </button>
        <ExtendMenu v-if="!p.indefinite" :row="row" />
        <button type="button" class="btn-outline" :disabled="!!busy || !canPing" :title="pingWhy" @click="run('ping')">
          <AppIcon :name="busy === 'ping' ? 'lucide:loader-circle' : 'lucide:send'" :spin="busy === 'ping'" /> Ping now
        </button>
        <SlotButtons name="plan-actions" :row="row" class="contents" />
        <button type="button" class="btn-danger ml-auto" :disabled="!!busy" @click="run('stop')">
          <AppIcon :name="busy === 'stop' ? 'lucide:loader-circle' : 'lucide:ban'" :spin="busy === 'stop'" /> Stop
        </button>
      </div>
    </div>
  </div>
</template>
