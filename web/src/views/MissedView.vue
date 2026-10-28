<script setup lang="ts">
import { onMounted } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import StatTile from '../components/StatTile.vue'
import { loadMissed, store } from '../api/store'
import { baseName, dateOnly, hoursLabel, money, parseTime, shortModel, signedMoney } from '../lib/format'
import { ui } from '../composables/ui'

onMounted(() => { if (!store.missed) loadMissed() })
</script>

<template>
  <div class="@container space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <h1 class="mr-auto text-base font-semibold text-ink">Missed savings</h1>
      <button type="button" class="icon-btn" aria-label="Recalculate" title="Recalculate" @click="loadMissed"><AppIcon name="lucide:refresh-cw" /></button>
    </div>

    <p v-if="!store.settings?.showMissed" class="card px-6 py-10 text-center text-[13px] text-ink-2">
      Missed savings are turned off.
      <button type="button" class="text-accent-ink underline underline-offset-2" @click="ui.settings = true">Turn them on in Settings.</button>
    </p>
    <p v-else-if="store.missedError" class="text-[13px] text-danger" role="alert">{{ store.missedError }}</p>
    <p v-else-if="!store.missed" class="flex items-center gap-2 py-10 text-ink-2" role="status"><AppIcon name="lucide:loader-circle" spin /> Replaying transcripts…</p>
    <template v-else>
      <p class="text-[13px] text-ink-1">
        If every idle session had been kept warm for up to {{ hoursLabel(store.missed.hours) }} (or its break-even) since
        {{ dateOnly(parseTime(store.missed.since) ?? 0) }}, replaying the live sessions' transcripts gives:
      </p>
      <div class="grid grid-cols-2 gap-2 @xl:grid-cols-4">
        <StatTile label="Would have saved" :value="money(store.missed.saved)" tone="ok">{{ store.missed.returns }} returns</StatTile>
        <StatTile label="Would have wasted" :value="money(store.missed.wasted)" :tone="store.missed.wasted ? 'danger' : 'neutral'">{{ store.missed.expired }} ran out</StatTile>
        <StatTile label="Net" :value="signedMoney(store.missed.net)" :tone="store.missed.net >= 0 ? 'ok' : 'danger'" class="col-span-2">a what-if, not money spent</StatTile>
      </div>
      <ul v-if="store.missed.sessions?.length" class="card divide-y divide-line">
        <li v-for="s in store.missed.sessions" :key="s.key" class="flex items-center gap-3 px-3.5 py-2.5">
          <div class="min-w-0 flex-1">
            <div class="truncate font-medium text-ink">{{ s.title || 'Untitled session' }}</div>
            <div class="text-xs text-ink-2">{{ baseName(s.project) }} · {{ shortModel(s.model) }} · {{ s.returns }} returns, {{ s.expired }} ran out</div>
            <div v-if="!s.known" class="text-xs text-warn">No price known for this model: not counted.</div>
          </div>
          <div class="text-right font-semibold tabular-nums" :class="s.net >= 0 ? 'text-ok' : 'text-danger'">{{ signedMoney(s.net) }}</div>
        </li>
      </ul>
      <p class="text-xs text-ink-2">Like the ledger, this assumes you would have come back at the same times anyway.</p>
    </template>
  </div>
</template>
