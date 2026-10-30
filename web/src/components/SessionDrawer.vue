<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import HarnessIcon from './HarnessIcon.vue'
import ModalDialog from './ModalDialog.vue'
import CacheBar from './CacheBar.vue'
import CacheCountdown from './CacheCountdown.vue'
import PlanPanel from './PlanPanel.vue'
import PlanStatus from './PlanStatus.vue'
import PingHistory from './PingHistory.vue'
import { drawerRow, ui } from '../composables/ui'
import { loadPings, pingNow, store } from '../api/store'
import { noReachWhy, planCap } from '../lib/derive'
import { clock, hoursLabel, money, parseTime, relative, shortModel, span, tokens } from '../lib/format'
import { now } from '../composables/now'

const r = drawerRow
const busy = ref(false)

watch(() => ui.drawer, key => { if (key) loadPings(key) }, { immediate: true })

const reach = computed(() => {
  const s = r.value?.s
  if (!s) return ''
  if (s.reach === 'socket') return 'Claude Code messaging socket'
  if (s.reach === 'queue') return 'codex queue'
  return 'Cannot be pinged'
})

const statusText = computed(() => {
  const s = r.value?.s
  if (!s) return ''
  if (s.harnessStatus === 'shell') return 'idle · background commands running'
  if (s.harnessStatus === 'waiting') return 'waiting for an answer'
  return s.status
})
const cap = computed(() => r.value ? planCap(r.value.s, store.settings) : undefined)
const t = (v: string | undefined) => parseTime(v)

async function ping () {
  if (!r.value) return
  busy.value = true
  await pingNow(r.value.key)
  busy.value = false
}
</script>

<template>
  <ModalDialog :open="!!r" side width="w-[30rem]" :title="r?.title ?? 'Session'" @close="ui.drawer = null">
    <template #icon><HarnessIcon v-if="r" :harness="r.s.harness" class="text-lg" /></template>
    <div v-if="r" class="space-y-5">
      <div>
        <div class="mono break-all text-ink-2">{{ r.s.cwd }}</div>
        <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1">
          <CacheCountdown :row="r" class="text-base font-semibold" />
          <span class="text-xs text-ink-2">TTL {{ span(r.ttlMs) }}</span>
        </div>
        <CacheBar :row="r" class="mt-1.5" />
      </div>

      <p v-if="r.s.reach === 'none'" class="flex gap-2 rounded-lg bg-danger-soft px-3 py-2 text-[13px] text-ink-1">
        <AppIcon name="lucide:wifi-off" class="mt-0.5 text-danger" />
        <span><strong class="text-ink">Can't be pinged.</strong> {{ noReachWhy(r.s) }}</span>
      </p>

      <section aria-labelledby="drawer-plan">
        <h3 id="drawer-plan" class="label mb-2">Keepalive</h3>
        <PlanStatus v-if="r.plan" :row="r" />
        <div v-else-if="r.s.reach !== 'none'" class="rounded-ka border border-line p-3.5">
          <PlanPanel :key="r.key" :row="r" @done="() => {}" @cancel="ui.drawer = null" />
          <div class="mt-3 border-t border-line pt-3">
            <button type="button" class="btn-outline" :disabled="busy || r.working" :title="r.working ? 'The session is working; it is pinged once idle.' : 'Send one keepalive ping now'" @click="ping">
              <AppIcon :name="busy ? 'lucide:loader-circle' : 'lucide:send'" :spin="busy" /> Ping once now
            </button>
          </div>
        </div>
      </section>

      <section aria-labelledby="drawer-details">
        <h3 id="drawer-details" class="label mb-2">Session</h3>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-2.5 text-[13px]">
          <div><dt class="text-ink-2">Model</dt><dd class="text-ink" :title="r.s.model">{{ shortModel(r.s.model) }}</dd></div>
          <div><dt class="text-ink-2">Status</dt><dd class="text-ink">{{ r.stale ? 'working (stale)' : statusText }}<span v-if="t(r.s.statusSince)" class="text-ink-2"> · {{ relative(t(r.s.statusSince)!, now) }}</span></dd></div>
          <div><dt class="text-ink-2">Context</dt><dd class="text-ink tabular-nums">{{ tokens(r.s.contextTokens) }} tokens</dd></div>
          <div><dt class="text-ink-2">You last typed</dt><dd class="text-ink">{{ r.humanAgoMs !== undefined ? span(r.humanAgoMs) + ' ago' : '—' }}</dd></div>
          <div><dt class="text-ink-2">Last API call</dt><dd class="text-ink">{{ t(r.s.lastCall) ? relative(t(r.s.lastCall)!, now) : '—' }}</dd></div>
          <div><dt class="text-ink-2">Started</dt><dd class="text-ink">{{ t(r.s.startedAt) ? clock(t(r.s.startedAt)!, now) : '—' }}</dd></div>
          <div><dt class="text-ink-2">Delivery</dt><dd class="text-ink">{{ reach }}</dd></div>
          <div><dt class="text-ink-2">Process</dt><dd class="mono text-ink">pid {{ r.s.pid }}</dd></div>
        </dl>
      </section>

      <section aria-labelledby="drawer-costs">
        <h3 id="drawer-costs" class="label mb-2">Costs<span v-if="!r.s.costs.known" class="ml-1 text-warn">(no price known for this model)</span></h3>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-2.5 text-[13px]">
          <div><dt class="text-ink-2">Re-cache (cold return)</dt><dd class="text-ink tabular-nums">{{ money(r.s.costs.recache) }}</dd></div>
          <div><dt class="text-ink-2">Each ping</dt><dd class="text-ink tabular-nums">{{ money(r.s.costs.ping) }}</dd></div>
          <div><dt class="text-ink-2">Ping every</dt><dd class="text-ink tabular-nums">{{ span(r.s.costs.pingInterval * 1000) }}</dd></div>
          <div>
            <dt class="text-ink-2">Break-even</dt>
            <dd class="text-ink tabular-nums" title="After this many hours of pings, keeping warm has cost as much as one re-cache">
              {{ r.s.costs.known && r.s.costs.breakEvenHours > 0 ? hoursLabel(r.s.costs.breakEvenHours) + ' of pings' : '—' }}
            </dd>
          </div>
          <div><dt class="text-ink-2">Saved here</dt><dd class="text-ok tabular-nums">{{ money(r.s.savedTotal) }}</dd></div>
          <div><dt class="text-ink-2">Wasted here</dt><dd class="tabular-nums" :class="r.s.wastedTotal ? 'text-danger' : 'text-ink'">{{ money(r.s.wastedTotal) }}</dd></div>
        </dl>
        <p class="mt-2 text-xs text-ink-2">
          List prices per 1M tokens: cache read {{ money(r.s.costs.readPerM) }}, cache write {{ money(r.s.costs.writePerM) }}, output {{ money(r.s.costs.outputPerM) }}.
          <template v-if="cap">Longest plan: {{ hoursLabel(cap.hours) }}.</template>
        </p>
      </section>

      <section aria-labelledby="drawer-pings">
        <div class="mb-2 flex items-center">
          <h3 id="drawer-pings" class="label">Ping history</h3>
          <button type="button" class="icon-btn ml-auto size-7" aria-label="Reload ping history" title="Reload" @click="loadPings(r.key)">
            <AppIcon name="lucide:refresh-cw" />
          </button>
        </div>
        <PingHistory :pings="store.pings[r.key]" />
      </section>
    </div>
  </ModalDialog>
</template>
