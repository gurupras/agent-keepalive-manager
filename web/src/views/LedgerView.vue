<script setup lang="ts">
import { onMounted } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import HarnessIcon from '../components/HarnessIcon.vue'
import StatTile from '../components/StatTile.vue'
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

const idle = (from: string, to: string) => span((parseTime(to) ?? 0) - (parseTime(from) ?? 0))

onMounted(() => { if (!store.ledger.entries.length) loadLedger() })
</script>

<template>
  <div class="@container space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <h1 class="mr-auto text-base font-semibold text-ink">Ledger</h1>
      <div role="radiogroup" aria-label="Range" class="inline-flex flex-wrap rounded-lg border border-line bg-surface p-0.5">
        <label
          v-for="r in ledgerRanges" :key="r.id"
          class="cursor-pointer rounded-md px-2.5 py-1 text-[13px] text-ink-2 has-[:checked]:bg-accent-soft has-[:checked]:font-medium has-[:checked]:text-accent-ink has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent"
        >
          <input type="radio" name="ledger-range" class="sr-only" :value="r.id" :checked="store.ledger.range === r.id" @change="setLedgerRange(r.id)" />{{ r.label }}
        </label>
      </div>
      <button type="button" class="icon-btn" aria-label="Reload ledger" title="Reload" :disabled="store.ledger.loading" @click="loadLedger">
        <AppIcon name="lucide:refresh-cw" :spin="store.ledger.loading" />
      </button>
    </div>

    <div class="grid grid-cols-2 gap-2 @xl:grid-cols-4">
      <StatTile label="Saved" :value="money(store.ledger.totals.saved)" tone="ok">{{ store.ledger.totals.returns }} return{{ store.ledger.totals.returns === 1 ? '' : 's' }} while warm</StatTile>
      <StatTile label="Wasted" :value="money(store.ledger.totals.wasted)" :tone="store.ledger.totals.wasted ? 'danger' : 'neutral'">{{ store.ledger.totals.expired }} ran out</StatTile>
      <StatTile label="Net" :value="signedMoney(store.ledger.totals.net)" :tone="store.ledger.totals.net >= 0 ? 'ok' : 'danger'">saved − wasted</StatTile>
      <StatTile label="Pings" :value="String(store.ledger.totals.pings)">{{ money(store.ledger.totals.pingCost) }} spent</StatTile>
    </div>

    <p class="flex gap-2 rounded-lg bg-info-soft px-3 py-2 text-xs text-ink-1">
      <AppIcon name="lucide:info" class="mt-px text-info" />
      <span>
        <strong class="text-ink">How "saved" is counted:</strong> a return is booked as saved only when its first call hit the warm cache, as the re-cache it avoided minus the pings it took.
        That assumes you would have come back at that time anyway; it is not a claim that keepalive made you come back sooner.
        Pings for absences nobody returned from in time are booked as wasted.
      </span>
    </p>

    <p v-if="store.ledger.error" class="text-[13px] text-danger" role="alert">{{ store.ledger.error }}</p>
    <div v-else-if="!store.ledger.entries.length" class="card px-6 py-12 text-center text-[13px] text-ink-2">
      {{ store.ledger.loading ? 'Loading…' : 'Nothing in this range yet. Entries appear when a kept session is returned to, runs out, or ends.' }}
    </div>
    <ul v-else class="card divide-y divide-line" aria-label="Ledger entries">
      <li v-for="e in store.ledger.entries" :key="e.id" class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 px-3.5 py-2.5 @2xl:grid-cols-[minmax(0,1fr)_14rem_8rem_6rem]">
        <div class="min-w-0">
          <div class="flex min-w-0 items-center gap-2">
            <HarnessIcon :harness="e.harness" />
            <span class="truncate font-medium text-ink" :title="e.title">{{ e.title || 'Untitled session' }}</span>
          </div>
          <div class="mt-0.5 flex flex-wrap gap-x-2 text-xs text-ink-2">
            <span :title="e.project">{{ baseName(e.project) }}</span>
            <span>{{ shortModel(e.model) }}</span>
            <span class="tabular-nums">{{ tokens(e.contextSize) }}</span>
          </div>
        </div>
        <div class="row-span-2 self-center text-right text-[15px] font-semibold tabular-nums @2xl:order-last @2xl:row-span-1" :class="e.net >= 0 ? 'text-ok' : 'text-danger'">
          {{ signedMoney(e.net) }}
        </div>
        <div class="text-xs text-ink-2 @2xl:text-[13px]">
          <span class="inline-flex items-center gap-1" :class="e.outcome === 'saved' ? 'text-ok' : 'text-ink-1'">
            <AppIcon :name="e.outcome === 'saved' ? 'lucide:circle-check' : e.endedBy === 'cold' ? 'lucide:snowflake' : 'lucide:circle-x'" />
            {{ endedBy[e.endedBy] ?? e.endedBy }}
          </span>
          <div class="text-ink-2 tabular-nums">{{ dateTime(parseTime(e.endedAt) ?? 0) }} · away {{ idle(e.idleFrom, e.endedAt) }}</div>
        </div>
        <div class="hidden text-right text-xs text-ink-2 tabular-nums @2xl:block">
          {{ e.pings }} ping{{ e.pings === 1 ? '' : 's' }} · {{ money(e.pingCost) }}
          <div v-if="e.avoided">avoided {{ money(e.avoided) }}</div>
        </div>
      </li>
    </ul>
  </div>
</template>
