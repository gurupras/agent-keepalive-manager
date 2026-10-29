<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import StatsStrip from '../components/StatsStrip.vue'
import StatusDot from '../components/StatusDot.vue'
import { pinned, projects, rows } from '../composables/ui'
import { countdown, money } from '../lib/format'
import { store } from '../api/store'

const router = useRouter()
const showIdle = ref(false)
const idleCount = computed(() => projects.value.filter(p => !p.active).length)
const shown = computed(() => projects.value.filter(p => p.active || showIdle.value))
const subtitle = computed(() => {
  const n = projects.value.length
  return `${n} project${n === 1 ? '' : 's'} with live sessions`
})

function open (id: string) {
  router.push({ name: 'project', params: { id } })
}
</script>

<template>
  <PageHeader title="All projects" :subtitle="subtitle" :mono="false" />
  <StatsStrip :rows="rows" />

  <div v-if="!store.loaded" class="flex items-center gap-2 py-16 text-ink-2" role="status">
    <AppIcon name="lucide:loader-circle" spin /> Loading sessions…
  </div>
  <div v-else-if="store.error && !rows.length" class="card mt-6 flex flex-col items-center gap-2 px-6 py-14 text-center" role="alert">
    <AppIcon name="lucide:wifi-off" class="text-3xl text-ink-3" />
    <h2 class="font-semibold text-ink">Can't reach agent-keepalive</h2>
    <p class="max-w-sm text-[13px] text-ink-2">{{ store.error }}</p>
    <p class="text-xs text-ink-2">Retrying in the background.</p>
  </div>
  <div v-else-if="!rows.length" class="card mt-6 flex flex-col items-center gap-2 px-6 py-14 text-center">
    <AppIcon name="lucide:activity" class="text-3xl text-ink-3" />
    <h2 class="font-semibold text-ink">No live sessions</h2>
    <p class="max-w-sm text-[13px] text-ink-2">Claude Code and Codex sessions running on this machine show up here within a few seconds of starting.</p>
  </div>

  <div v-else class="card @container mt-6 overflow-hidden">
    <table class="w-full">
      <thead class="text-left text-xs text-ink-2">
        <tr class="border-b border-line">
          <th class="px-5 py-2.5 font-medium">Project</th>
          <th class="hidden py-2.5 font-medium @xl:table-cell">Sessions</th>
          <th class="hidden py-2.5 font-medium @3xl:table-cell">Next to go cold</th>
          <th class="py-2.5 text-right font-medium">Warm cache</th>
          <th class="w-10"><span class="sr-only">Open</span></th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="p in shown" :key="p.id"
          class="cursor-pointer border-t border-line outline-none first:border-t-0 hover:bg-surface-2/40 focus-visible:bg-surface-2"
          tabindex="0"
          @click="open(p.id)"
          @keydown.enter="open(p.id)"
        >
          <td class="max-w-0 px-5 py-3.5">
            <div class="flex min-w-0 items-center gap-3" :title="p.id">
              <StatusDot :state="p.dot" />
              <span class="truncate font-medium text-ink">{{ p.name }}</span>
              <AppIcon v-if="pinned[p.id]" name="lucide:pin" class="shrink-0 text-xs text-accent" label="Pinned" />
            </div>
            <div class="mt-0.5 pl-5 text-xs text-ink-2 @xl:hidden">{{ p.line }}</div>
          </td>
          <td class="hidden py-3.5 text-ink-1 @xl:table-cell">{{ p.line }}</td>
          <td class="hidden max-w-0 py-3.5 pr-4 @3xl:table-cell">
            <div v-if="p.nextCold" class="truncate" :class="p.nextCold.leftMs < 15 * 60_000 ? 'font-medium text-warn' : 'text-ink-1'">
              <span class="tabular-nums">{{ countdown(p.nextCold.leftMs) }}</span> · <span class="font-normal text-ink-2">{{ p.nextCold.title }}</span>
            </div>
            <span v-else class="text-ink-3">—</span>
          </td>
          <td class="py-3.5 text-right text-ink tabular-nums">{{ p.warmValue ? money(p.warmValue) : '—' }}</td>
          <td class="pr-4 text-right text-ink-3"><AppIcon name="lucide:chevron-right" /></td>
        </tr>
        <tr v-if="!shown.length">
          <td colspan="5" class="px-5 py-8 text-center text-ink-2">Nothing is warm right now.</td>
        </tr>
      </tbody>
    </table>
    <button
      v-if="idleCount"
      type="button"
      class="flex w-full cursor-pointer items-center gap-2 border-t border-line px-5 py-2.5 text-left text-[13px] text-ink-2 outline-none hover:bg-surface-2/40 focus-visible:bg-surface-2"
      :aria-expanded="showIdle"
      @click="showIdle = !showIdle"
    >
      <AppIcon :name="showIdle ? 'lucide:chevron-up' : 'lucide:chevron-down'" />
      {{ showIdle ? 'Hide' : 'Show' }} {{ idleCount }} project{{ idleCount === 1 ? '' : 's' }} with only cold sessions
    </button>
  </div>
</template>
