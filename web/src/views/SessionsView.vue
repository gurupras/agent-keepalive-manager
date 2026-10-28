<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from '../components/AppIcon.vue'
import ProjectGroup from '../components/ProjectGroup.vue'
import { projects, rows } from '../composables/ui'
import { store } from '../api/store'
import { money } from '../lib/format'

const kept = computed(() => rows.value.filter(r => r.kept).length)
const paused = computed(() => rows.value.filter(r => r.paused).length)
const blocked = computed(() => rows.value.filter(r => r.plan?.blocked).length)
/** what re-caching every warm, unkept session would cost if it went cold */
const atRisk = computed(() => rows.value.filter(r => r.warm && !r.kept && !r.working).reduce((a, r) => a + r.s.costs.recache, 0))
</script>

<template>
  <div class="space-y-4">
    <div v-if="rows.length" class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-ink-2">
      <span><strong class="text-ink tabular-nums">{{ rows.length }}</strong> live session{{ rows.length === 1 ? '' : 's' }}</span>
      <span class="inline-flex items-center gap-1"><AppIcon name="lucide:flame" class="text-warm" /><strong class="text-ink tabular-nums">{{ kept }}</strong> kept warm</span>
      <span v-if="paused" class="inline-flex items-center gap-1 text-warn"><AppIcon name="lucide:pause" /><strong class="tabular-nums">{{ paused }}</strong> paused</span>
      <span v-if="blocked" class="inline-flex items-center gap-1 text-warn"><AppIcon name="lucide:hourglass" /><strong class="tabular-nums">{{ blocked }}</strong> blocked</span>
      <span v-if="atRisk > 0" title="What re-caching would cost if every warm session nobody keeps warm went cold">
        <strong class="text-ink tabular-nums">{{ money(atRisk) }}</strong> of warm cache not kept
      </span>
    </div>

    <div v-if="!store.loaded" class="flex items-center gap-2 py-16 text-ink-2" role="status">
      <AppIcon name="lucide:loader-circle" spin /> Loading sessions…
    </div>
    <div v-else-if="store.error && !rows.length" class="card flex flex-col items-center gap-2 px-6 py-14 text-center" role="alert">
      <AppIcon name="lucide:wifi-off" class="text-3xl text-ink-3" />
      <h2 class="font-semibold text-ink">Can't reach agent-keepalive</h2>
      <p class="max-w-sm text-[13px] text-ink-2">{{ store.error }}</p>
      <p class="text-xs text-ink-2">Retrying in the background.</p>
    </div>
    <div v-else-if="!rows.length" class="card flex flex-col items-center gap-2 px-6 py-14 text-center">
      <AppIcon name="lucide:activity" class="text-3xl text-ink-3" />
      <h2 class="font-semibold text-ink">No live sessions</h2>
      <p class="max-w-sm text-[13px] text-ink-2">Claude Code and Codex sessions running on this machine show up here within a few seconds of starting.</p>
    </div>
    <div v-else class="@container space-y-4">
      <ProjectGroup v-for="g in projects" :key="g.id" :group="g" />
    </div>
  </div>
</template>
