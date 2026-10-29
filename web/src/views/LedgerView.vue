<script setup lang="ts">
import { computed, onMounted } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import HarnessIcon from '../components/HarnessIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import type { LedgerEntry } from '../api/types'
import type { IconName } from '../icons/names'
import { ledgerRanges, loadLedger, setLedgerRange, store } from '../api/store'
import { baseName, dateTime, money, parseTime, shortModel, signedMoney, span, tokens } from '../lib/format'

const endedBy: Record<string, string> = {
  returned: 'You came back',
  expired: 'Plan ran out',
  cancelled: 'Stopped by you',
  exited: 'Session exited',
  switched: 'Conversation switched',
  cold: 'Cache found cold',
  invalidated: 'Return missed the cache',
}

function icon (e: LedgerEntry): IconName {
  if (e.outcome === 'saved') return 'lucide:circle-check'
  return e.endedBy === 'cold' ? 'lucide:snowflake' : 'lucide:circle-x'
}

function badge (e: LedgerEntry): string {
  if (e.outcome === 'saved') return 'bg-ok-soft text-ok'
  return e.endedBy === 'cold' ? 'bg-cold-soft text-cold' : 'bg-surface-2 text-ink-1'
}

const away = (from: string, to: string) => span((parseTime(to) ?? 0) - (parseTime(from) ?? 0))

const range = computed(() => ledgerRanges.find(r => r.id === store.ledger.range)?.label ?? '')
const n = computed(() => store.ledger.entries.length)

onMounted(() => { if (!store.ledger.entries.length) loadLedger() })
</script>

<template>
  <PageHeader title="Ledger" :subtitle="`${n} entr${n === 1 ? 'y' : 'ies'} · ${range}`" :mono="false">
    <div role="radiogroup" aria-label="Range" class="inline-flex flex-wrap rounded-lg bg-surface-2 p-0.5">
      <label
        v-for="r in ledgerRanges" :key="r.id"
        class="cursor-pointer rounded-md px-2.5 py-1 text-[13px] text-ink-2 hover:text-ink has-[:checked]:bg-surface has-[:checked]:font-medium has-[:checked]:text-ink has-[:checked]:shadow-sm has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent"
      >
        <input type="radio" name="ledger-range" class="sr-only" :value="r.id" :checked="store.ledger.range === r.id" @change="setLedgerRange(r.id)" />{{ r.label }}
      </label>
    </div>
    <button type="button" class="icon-btn" aria-label="Reload ledger" title="Reload" :disabled="store.ledger.loading" @click="loadLedger">
      <AppIcon name="lucide:refresh-cw" :spin="store.ledger.loading" />
    </button>
  </PageHeader>

  <div class="card mt-5 flex flex-wrap gap-x-10 gap-y-3 px-5 py-3.5">
    <div>
      <div class="label">Net</div>
      <div class="text-xl font-semibold tabular-nums" :class="store.ledger.totals.net >= 0 ? 'text-ok' : 'text-danger'">{{ signedMoney(store.ledger.totals.net) }}</div>
    </div>
    <div>
      <div class="label">Saved</div>
      <div class="text-xl font-semibold text-ok tabular-nums">{{ money(store.ledger.totals.saved) }}</div>
    </div>
    <div>
      <div class="label">Wasted</div>
      <div class="text-xl font-semibold tabular-nums" :class="store.ledger.totals.wasted ? 'text-danger' : 'text-ink'">{{ money(store.ledger.totals.wasted) }}</div>
    </div>
    <div>
      <div class="label">Pings</div>
      <div class="text-xl font-semibold text-ink tabular-nums">{{ store.ledger.totals.pings }} <span class="text-sm font-normal text-ink-2">· {{ money(store.ledger.totals.pingCost) }}</span></div>
    </div>
    <div>
      <div class="label">Came back warm</div>
      <div class="text-xl font-semibold text-ink tabular-nums">{{ store.ledger.totals.returns }}</div>
    </div>
    <div>
      <div class="label">Ran out</div>
      <div class="text-xl font-semibold text-ink tabular-nums">{{ store.ledger.totals.expired }}</div>
    </div>
  </div>

  <p v-if="store.ledger.error" class="mt-6 text-[13px] text-danger" role="alert">{{ store.ledger.error }}</p>
  <div v-else-if="!n" class="card mt-6 px-6 py-12 text-center text-[13px] text-ink-2">
    {{ store.ledger.loading ? 'Loading…' : 'Nothing in this range yet. Entries appear when a kept session is returned to, runs out, or ends.' }}
  </div>
  <div v-else class="card @container mt-6 overflow-hidden">
    <table class="hidden w-full table-fixed @3xl:table" aria-label="Ledger entries">
      <thead class="text-left text-xs text-ink-2">
        <tr class="border-b border-line">
          <th class="px-5 py-2.5 font-medium">Session</th>
          <th class="w-44 py-2.5 font-medium">Window</th>
          <th class="w-52 py-2.5 font-medium">Ended by</th>
          <th class="w-16 py-2.5 text-right font-medium">Pings</th>
          <th class="w-24 py-2.5 text-right font-medium">Ping cost</th>
          <th class="w-24 py-2.5 text-right font-medium">Avoided</th>
          <th class="w-28 py-2.5 pr-5 text-right font-medium">Net</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="e in store.ledger.entries" :key="e.id" class="border-t border-line first:border-t-0">
          <td class="max-w-0 px-5 py-3">
            <div class="flex min-w-0 items-center gap-2">
              <HarnessIcon :harness="e.harness" />
              <span class="truncate font-medium text-ink" :title="e.title">{{ e.title || 'Untitled session' }}</span>
            </div>
            <div class="mt-0.5 truncate text-xs text-ink-2"><span :title="e.project">{{ baseName(e.project) }}</span> · {{ shortModel(e.model) }} · <span class="tabular-nums">{{ tokens(e.contextSize) }}</span></div>
          </td>
          <td class="py-3 pr-4 text-[13px] text-ink-1 tabular-nums">
            {{ dateTime(parseTime(e.endedAt) ?? 0) }}
            <div class="text-xs text-ink-2">away {{ away(e.idleFrom, e.endedAt) }}</div>
          </td>
          <td class="py-3 pr-4"><span class="badge" :class="badge(e)"><AppIcon :name="icon(e)" />{{ endedBy[e.endedBy] ?? e.endedBy }}</span></td>
          <td class="py-3 text-right text-ink-1 tabular-nums">{{ e.pings }}</td>
          <td class="py-3 text-right text-ink-1 tabular-nums">{{ money(e.pingCost) }}</td>
          <td class="py-3 text-right text-ink-1 tabular-nums">{{ e.avoided ? money(e.avoided) : '—' }}</td>
          <td class="py-3 pr-5 text-right font-semibold tabular-nums" :class="e.net >= 0 ? 'text-ok' : 'text-danger'">{{ signedMoney(e.net) }}</td>
        </tr>
      </tbody>
    </table>
    <ul class="divide-y divide-line @3xl:hidden" aria-label="Ledger entries">
      <li v-for="e in store.ledger.entries" :key="e.id" class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 px-3.5 py-2.5">
        <div class="min-w-0">
          <div class="flex min-w-0 items-center gap-2">
            <HarnessIcon :harness="e.harness" />
            <span class="truncate font-medium text-ink" :title="e.title">{{ e.title || 'Untitled session' }}</span>
          </div>
          <div class="mt-0.5 truncate text-xs text-ink-2">{{ baseName(e.project) }} · {{ shortModel(e.model) }} · {{ tokens(e.contextSize) }}</div>
        </div>
        <div class="self-center text-right text-[15px] font-semibold tabular-nums" :class="e.net >= 0 ? 'text-ok' : 'text-danger'">{{ signedMoney(e.net) }}</div>
        <div class="col-span-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-ink-2">
          <span class="badge" :class="badge(e)"><AppIcon :name="icon(e)" />{{ endedBy[e.endedBy] ?? e.endedBy }}</span>
          <span class="tabular-nums">{{ dateTime(parseTime(e.endedAt) ?? 0) }} · away {{ away(e.idleFrom, e.endedAt) }} · {{ e.pings }} ping{{ e.pings === 1 ? '' : 's' }}, {{ money(e.pingCost) }}</span>
        </div>
      </li>
    </ul>
  </div>

  <p class="mt-4 flex gap-2 text-xs text-ink-2">
    <AppIcon name="lucide:info" class="mt-px shrink-0 text-info" />
    <span>
      A return is booked as saved only when its first call hit the warm cache, as the re-cache it avoided minus the pings it took.
      That assumes you would have come back at that time anyway; it is not a claim that keepalive made you come back sooner.
      Pings for absences nobody returned from in time are booked as wasted.
    </span>
  </p>
</template>

<style scoped>
@reference "../style.css";

.badge {
  @apply inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium whitespace-nowrap;
}
</style>
