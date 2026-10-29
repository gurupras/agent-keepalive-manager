<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import ColdList from '../components/ColdList.vue'
import PageHeader from '../components/PageHeader.vue'
import SessionCard from '../components/SessionCard.vue'
import SessionTable from '../components/SessionTable.vue'
import StatsStrip from '../components/StatsStrip.vue'
import ViewToggle from '../components/ViewToggle.vue'
import { pinned, projects, togglePin, viewMode } from '../composables/ui'
import { coldOrder, isLive } from '../lib/derive'
import { baseName } from '../lib/format'
import { store } from '../api/store'

const props = defineProps<{ id: string }>()

const project = computed(() => projects.value.find(p => p.id === props.id))
const rows = computed(() => project.value?.rows ?? [])
const live = computed(() => rows.value.filter(isLive))
const cold = computed(() => rows.value.filter(r => !isLive(r)).sort(coldOrder))
</script>

<template>
  <PageHeader :title="project?.name ?? baseName(id)" :subtitle="id">
    <ViewToggle />
    <button type="button" class="btn-ghost" :aria-pressed="!!pinned[id]" @click="togglePin(id)">
      <AppIcon :name="pinned[id] ? 'lucide:pin-off' : 'lucide:pin'" />{{ pinned[id] ? 'Unpin' : 'Pin' }}
    </button>
  </PageHeader>

  <StatsStrip :rows="rows" :project="id" />

  <div class="mt-6">
    <div v-if="!project" class="card px-6 py-10 text-center text-[13px] text-ink-2">
      {{ store.loaded ? 'No live sessions in this project.' : 'Loading…' }}
    </div>
    <div v-else-if="!live.length" class="card flex flex-col items-center gap-2 px-6 py-10 text-center text-[13px] text-ink-2">
      <AppIcon name="lucide:snowflake" class="text-2xl text-cold" />
      Nothing warm in this project right now.
    </div>
    <div v-else-if="viewMode === 'cards'" class="grid gap-3 @4xl/app:grid-cols-2">
      <SessionCard v-for="r in live" :key="r.key" :row="r" />
    </div>
    <div v-else class="card">
      <SessionTable :rows="live" />
    </div>
    <ColdList :key="id" :rows="cold" />
  </div>
</template>
