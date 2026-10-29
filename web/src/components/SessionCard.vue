<script setup lang="ts">
import CacheBar from './CacheBar.vue'
import CacheCountdown from './CacheCountdown.vue'
import ContextInfo from './ContextInfo.vue'
import KeepControl from './KeepControl.vue'
import PlanPanel from './PlanPanel.vue'
import SessionTitle from './SessionTitle.vue'
import type { Row } from '../lib/derive'
import { span } from '../lib/format'
import { ui } from '../composables/ui'

defineProps<{ row: Row, showProject?: boolean }>()
</script>

<template>
  <div class="card @container flex flex-col p-4" :class="row.kept ? 'ring-1 ring-warm/35' : row.paused ? 'ring-1 ring-warn/35' : ''">
    <SessionTitle :row="row" :show-project="showProject" />
    <p class="mt-1 line-clamp-2 min-h-[2lh] text-[13px] leading-snug text-ink-2">{{ row.s.summary || 'No summary yet' }}</p>

    <div class="mt-4 grid grid-cols-[auto_1fr] items-end gap-x-6">
      <div>
        <div class="label">Context</div>
        <div class="mt-0.5 text-lg font-semibold text-ink"><ContextInfo :s="row.s" /></div>
      </div>
      <div>
        <div class="flex items-baseline justify-between gap-2">
          <span class="label">Cache expires in</span>
          <CacheCountdown :row="row" class="text-[13px]" />
        </div>
        <CacheBar :row="row" class="mt-1.5" />
      </div>
    </div>

    <div class="mt-4 border-t border-line pt-3">
      <KeepControl v-if="row.plan" :row="row" />
      <PlanPanel v-else-if="ui.planFor === row.key" :row="row" @done="ui.planFor = null" @cancel="ui.planFor = null" />
      <div v-else class="flex items-center gap-2">
        <span class="text-[13px] text-ink-2">{{ row.humanAgoMs !== undefined ? `You last typed ${span(row.humanAgoMs)} ago` : '' }}</span>
        <div class="flex-1"></div>
        <KeepControl :row="row" />
      </div>
    </div>
  </div>
</template>
