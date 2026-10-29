<script setup lang="ts">
import { computed, onMounted, useTemplateRef } from 'vue'
import { useRoute } from 'vue-router'
import AppIcon from './AppIcon.vue'
import AppMenu from './AppMenu.vue'
import LinkStatus from './LinkStatus.vue'
import StatusDot from './StatusDot.vue'
import { pinned, projects, rows, searchInput, ui } from '../composables/ui'
import { dark, toggleDark } from '../composables/theme'
import { store } from '../api/store'

const route = useRoute()
const input = useTemplateRef<HTMLInputElement>('search')
onMounted(() => { searchInput.value = input.value })
const current = computed(() => route.name === 'project' ? String(route.params.id) : null)

const filtered = computed(() => {
  const q = ui.search.trim().toLowerCase()
  return projects.value.filter(p => !q || p.id.toLowerCase().includes(q))
})
const pinnedProjects = computed(() => filtered.value.filter(p => pinned.value[p.id]))
const otherProjects = computed(() => filtered.value.filter(p => !pinned.value[p.id]))

function onSearchKey (e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    ui.search = ''
    ;(e.target as HTMLInputElement).blur()
  }
}
</script>

<template>
  <aside class="flex w-72 shrink-0 flex-col border-r border-line bg-base" aria-label="Navigation">
    <div class="flex items-center gap-1 px-4 pt-5 pb-3">
      <AppIcon name="lucide:heart-pulse" class="mr-1 text-xl text-accent" />
      <span class="text-[15px] font-semibold text-ink">keepalive</span>
      <div class="flex-1"></div>
      <button type="button" class="icon-btn" title="Settings" aria-label="Settings" @click="ui.settings = true">
        <AppIcon name="lucide:settings" />
      </button>
      <button type="button" class="icon-btn" :title="dark ? 'Light theme' : 'Dark theme'" :aria-label="dark ? 'Switch to the light theme' : 'Switch to the dark theme'" @click="toggleDark">
        <AppIcon :name="dark ? 'lucide:sun' : 'lucide:moon'" />
      </button>
      <AppMenu />
    </div>
    <div class="px-3">
      <div class="relative">
        <AppIcon name="lucide:search" class="pointer-events-none absolute top-2.5 left-2.5 text-ink-3" />
        <input
          ref="search"
          v-model="ui.search"
          type="search"
          placeholder="Find a project  ( / )"
          aria-label="Find a project"
          class="input pl-8"
          @keydown="onSearchKey"
        />
      </div>
    </div>
    <nav class="mt-3 flex-1 space-y-0.5 overflow-y-auto px-3 pb-6" aria-label="Views">
      <RouterLink to="/" class="nav-item" :class="{ on: route.name === 'all' }">
        <AppIcon name="lucide:layout-grid" class="text-ink-3" /> All projects
      </RouterLink>
      <RouterLink to="/sessions" class="nav-item" :class="{ on: route.name === 'sessions' }">
        <AppIcon name="lucide:layout-list" class="text-ink-3" /> Sessions
        <span v-if="rows.length" class="ml-auto text-xs text-ink-3 tabular-nums">{{ rows.length }}</span>
      </RouterLink>
      <RouterLink to="/ledger" class="nav-item" :class="{ on: route.name === 'ledger' }">
        <AppIcon name="lucide:receipt" class="text-ink-3" /> Ledger
      </RouterLink>
      <RouterLink v-if="store.settings?.showMissed" to="/missed" class="nav-item" :class="{ on: route.name === 'missed' }">
        <AppIcon name="lucide:lightbulb" class="text-ink-3" /> Missed savings
      </RouterLink>
      <template v-if="pinnedProjects.length">
        <div class="label px-3 pt-4 pb-1">Pinned</div>
        <RouterLink
          v-for="p in pinnedProjects" :key="p.id"
          :to="{ name: 'project', params: { id: p.id } }"
          class="nav-item" :class="{ on: current === p.id }" :title="p.id"
        >
          <StatusDot :state="p.dot" /><span class="truncate">{{ p.name }}</span>
          <span class="ml-auto text-xs text-ink-3 tabular-nums">{{ p.rows.length }}</span>
        </RouterLink>
      </template>
      <div class="label px-3 pt-4 pb-1">{{ pinnedProjects.length ? 'Other projects' : 'Projects' }}</div>
      <RouterLink
        v-for="p in otherProjects" :key="p.id"
        :to="{ name: 'project', params: { id: p.id } }"
        class="nav-item" :class="{ on: current === p.id }" :title="p.id"
      >
        <StatusDot :state="p.dot" /><span class="truncate">{{ p.name }}</span>
        <span class="ml-auto text-xs text-ink-3 tabular-nums">{{ p.rows.length }}</span>
      </RouterLink>
      <div v-if="!filtered.length" class="px-3 py-2 text-[13px] text-ink-3">
        {{ ui.search ? 'No matching projects' : store.loaded ? 'No live sessions' : 'Loading…' }}
      </div>
    </nav>
    <div class="space-y-2 border-t border-line px-4 py-3 text-xs text-ink-2">
      <div class="flex flex-wrap gap-x-3 gap-y-1">
        <span class="inline-flex items-center gap-1.5"><StatusDot state="kept" />kept warm</span>
        <span class="inline-flex items-center gap-1.5"><StatusDot state="warm" />warm</span>
        <span class="inline-flex items-center gap-1.5"><StatusDot state="cold" />cold</span>
      </div>
      <LinkStatus />
    </div>
  </aside>
</template>

<style scoped>
@reference "../style.css";

.nav-item {
  @apply flex items-center gap-2.5 rounded-lg px-3 py-1.5 text-[13px] text-ink-1 outline-none hover:bg-surface-2 focus-visible:ring-2 focus-visible:ring-accent;
}

.nav-item.on {
  @apply bg-surface font-medium text-ink shadow-sm ring-1 ring-line;
}
</style>
