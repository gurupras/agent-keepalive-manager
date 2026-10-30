<script setup lang="ts">
import { computed } from 'vue'
import PingStateBadge from './PingStateBadge.vue'
import type { PingRecord } from '../api/types'
import { dateTime, money, parseTime, relative, tokens } from '../lib/format'
import { now } from '../composables/now'

const props = defineProps<{ pings: PingRecord[] | undefined }>()
const list = computed(() => props.pings ?? [])
</script>

<template>
  <div>
    <p v-if="!pings" class="text-[13px] text-ink-2">Loading…</p>
    <p v-else-if="!list.length" class="text-[13px] text-ink-2">No pings yet.</p>
    <ol v-else class="divide-y divide-line rounded-ka border border-line">
      <li v-for="p in list" :key="p.id" class="px-3 py-2">
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
          <PingStateBadge :state="p.state" :cold="p.cold" />
          <span v-if="p.experiment" class="rounded bg-surface-2 px-1.5 text-[11px] text-ink-2" title="Sent by an experiment while the session was working but quiet (docs/experiments.md)">experiment</span>
          <time class="text-xs text-ink-2 tabular-nums" :datetime="p.sentAt" :title="dateTime(parseTime(p.sentAt) ?? 0)">{{ relative(parseTime(p.sentAt) ?? 0, now) }}</time>
          <span class="ml-auto text-xs text-ink tabular-nums">{{ p.cost ? money(p.cost) : '' }}</span>
        </div>
        <div v-if="p.state === 'verified'" class="mt-0.5 text-[11px] text-ink-2 tabular-nums">
          {{ p.calls }} API call{{ p.calls === 1 ? '' : 's' }} · read {{ tokens(p.usage.cacheRead) }} · wrote {{ tokens(p.usage.cacheWrite5m + p.usage.cacheWrite1h) }}
          <span v-if="p.calls > 1" class="text-warn">(more than one call)</span>
        </div>
        <div v-if="p.error" class="mt-0.5 text-xs text-danger">{{ p.error }}</div>
      </li>
    </ol>
  </div>
</template>
