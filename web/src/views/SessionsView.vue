<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import SessionCard from '../components/SessionCard.vue'
import SessionTable from '../components/SessionTable.vue'
import StatsStrip from '../components/StatsStrip.vue'
import ViewToggle from '../components/ViewToggle.vue'
import { rows, viewMode } from '../composables/ui'
import { coldOrder, isLive, sortRows } from '../lib/derive'
import { store } from '../api/store'

// Every session across all projects: kept first, then working, then the
// soonest to go cold, then cold sessions by most recently used.
const query = ref('')
const all = computed(() => [
  ...rows.value.filter(isLive).sort(sortRows),
  ...rows.value.filter(r => !isLive(r)).sort(coldOrder),
])
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return all.value
  return all.value.filter(r => [r.title, r.s.summary, r.s.project, r.s.cwd, r.s.model, r.s.id].some(f => f?.toLowerCase().includes(q)))
})
const subtitle = computed(() => {
  const n = all.value.length
  const w = all.value.filter(isLive).length
  const p = new Set(all.value.map(r => r.s.project || r.s.cwd)).size
  return n ? `${n} session${n === 1 ? '' : 's'} in ${p} project${p === 1 ? '' : 's'} · ${w} warm` : 'No live sessions'
})
</script>

<template>
  <PageHeader title="Sessions" :subtitle="subtitle" :mono="false">
    <label class="flex items-center gap-2 rounded-lg bg-surface-2 px-2.5 py-1.5 text-[13px] text-ink-2 focus-within:ring-2 focus-within:ring-accent">
      <AppIcon name="lucide:search" class="text-ink-3" />
      <input v-model="query" type="search" placeholder="Find a session" aria-label="Find a session" class="w-44 bg-transparent text-ink outline-none placeholder:text-ink-3" @keydown.esc.stop="query = ''" />
    </label>
    <ViewToggle />
  </PageHeader>

  <StatsStrip :rows="rows" />

  <div class="mt-6">
    <div v-if="!store.loaded" class="flex items-center gap-2 py-16 text-ink-2" role="status">
      <AppIcon name="lucide:loader-circle" spin /> Loading sessions…
    </div>
    <div v-else-if="!shown.length" class="card flex flex-col items-center gap-2 px-6 py-10 text-center text-[13px] text-ink-2">
      <AppIcon :name="query ? 'lucide:search' : 'lucide:activity'" class="text-2xl text-ink-3" />
      {{ query ? `No sessions match “${query}”.` : 'No live sessions. Claude Code and Codex sessions running on this machine show up here within a few seconds of starting.' }}
    </div>
    <div v-else-if="viewMode === 'cards'" class="grid gap-3 @4xl/app:grid-cols-2">
      <SessionCard v-for="r in shown" :key="r.key" :row="r" show-project />
    </div>
    <div v-else class="card">
      <SessionTable :rows="shown" show-project />
    </div>
  </div>
</template>
