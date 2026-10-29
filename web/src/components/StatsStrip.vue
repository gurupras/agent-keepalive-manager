<script setup lang="ts">
import { computed } from 'vue'
import { ledgerRanges, store } from '../api/store'
import { sumEntries, type Row } from '../lib/derive'
import { money, signedMoney } from '../lib/format'

// The headline numbers over `rows`; with `project`, the ledger and the
// missed savings are scoped to it too.
const props = defineProps<{ rows: Row[], project?: string }>()

const range = computed(() => ledgerRanges.find(r => r.id === store.ledger.range)?.label ?? '')
const totals = computed(() => props.project
  ? sumEntries(store.ledger.entries.filter(e => e.project === props.project))
  : store.ledger.totals)
const kept = computed(() => props.rows.filter(r => r.kept || r.paused).length)
const warm = computed(() => props.rows.filter(r => r.warm && !r.kept && !r.paused).length)
const warmValue = computed(() => props.rows.filter(r => r.warm).reduce((a, r) => a + r.s.costs.recache, 0))
const missed = computed(() => {
  const m = store.missed
  if (!m || !store.settings?.showMissed) return undefined
  if (!props.project) return m.net
  return (m.sessions ?? []).filter(s => s.project === props.project).reduce((a, s) => a + s.net, 0)
})
</script>

<template>
  <div class="card mt-5 flex flex-wrap gap-x-10 gap-y-3 px-5 py-3.5">
    <div :title="`Booked in the ledger, ${range.toLowerCase()}`">
      <div class="label">Saved <span class="font-normal text-ink-3">· {{ range }}</span></div>
      <div class="text-xl font-semibold text-ok tabular-nums">{{ money(totals.saved) }}</div>
    </div>
    <div :title="`Pings for absences nobody returned from in time, ${range.toLowerCase()}`">
      <div class="label">Wasted</div>
      <div class="text-xl font-semibold tabular-nums" :class="totals.wasted > 0 ? 'text-danger' : 'text-ink'">{{ money(totals.wasted) }}</div>
    </div>
    <div>
      <div class="label">Keeping warm</div>
      <div class="text-xl font-semibold tabular-nums" :class="kept ? 'text-warm' : 'text-ink'">{{ kept }}</div>
    </div>
    <div title="Warm sessions nobody keeps warm">
      <div class="label">Warm now</div>
      <div class="text-xl font-semibold text-ink tabular-nums">{{ warm }}</div>
    </div>
    <div title="What re-caching every warm session would cost">
      <div class="label">Warm cache value</div>
      <div class="text-xl font-semibold text-ink tabular-nums">{{ money(warmValue) }}</div>
    </div>
    <RouterLink
      v-if="missed !== undefined" to="/missed"
      class="-m-1 rounded-lg p-1 outline-none hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-accent"
      title="If every idle session had been kept warm. Details under Missed."
    >
      <div class="label">Could have saved</div>
      <div class="text-xl font-semibold tabular-nums" :class="missed > 0 ? 'text-warn' : 'text-ink'">{{ signedMoney(missed) }}</div>
    </RouterLink>
  </div>
</template>
