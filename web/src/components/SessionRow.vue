<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import HarnessIcon from './HarnessIcon.vue'
import CacheBar from './CacheBar.vue'
import CacheCountdown from './CacheCountdown.vue'
import PlanPanel from './PlanPanel.vue'
import PlanStatus from './PlanStatus.vue'
import { noReachWhy, type Row } from '../lib/derive'
import { money, shortModel, tokens } from '../lib/format'
import { ui } from '../composables/ui'

const props = defineProps<{ row: Row }>()

const panelOpen = computed(() => ui.planFor === props.row.key && !props.row.plan)
const unreachable = computed(() => props.row.s.reach === 'none')

function togglePanel () {
  ui.planFor = panelOpen.value ? null : props.row.key
}
</script>

<template>
  <!-- One grid that stacks in narrow containers (a side panel) and spreads
       into columns from about 42rem. -->
  <li class="border-t border-line first:border-t-0">
    <div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-3.5 py-3 @2xl:grid-cols-[minmax(0,1fr)_5.5rem_9rem_minmax(0,19rem)]">
      <div class="col-span-2 min-w-0 @2xl:col-span-1">
        <div class="flex min-w-0 items-center gap-2">
          <HarnessIcon :harness="row.s.harness" class="text-base" />
          <button
            type="button"
            class="min-w-0 cursor-pointer truncate rounded text-left font-medium text-ink outline-none hover:underline focus-visible:ring-2 focus-visible:ring-accent"
            :title="row.s.cwd"
            @click="ui.drawer = row.key"
          >{{ row.title }}</button>
          <AppIcon v-if="unreachable" name="lucide:wifi-off" class="text-ink-2" :label="'Cannot be pinged: ' + noReachWhy(row.s)" />
        </div>
        <div class="mt-0.5 flex min-w-0 flex-wrap items-center gap-1.5 text-xs text-ink-2">
          <span class="chip" :class="row.s.costs.known ? '' : 'text-warn'" :title="row.s.costs.known ? row.s.model : `${row.s.model}: no known price`">{{ shortModel(row.s.model) }}</span>
          <span v-if="row.where !== '.'" class="chip mono max-w-[14rem]" :title="row.s.cwd"><span class="truncate">{{ row.where }}</span></span>
          <span class="tabular-nums @2xl:hidden">{{ tokens(row.s.contextTokens) }} context</span>
          <span v-if="row.warm" class="tabular-nums @2xl:hidden" title="What a cold return would cost: re-writing the context to the cache">· re-cache {{ money(row.s.costs.recache) }}</span>
        </div>
      </div>

      <div class="hidden text-right tabular-nums @2xl:block">
        <div class="text-ink">{{ tokens(row.s.contextTokens) }}</div>
        <div class="text-xs text-ink-2" title="What a cold return would cost: re-writing the context to the cache">{{ money(row.s.costs.recache) }}</div>
      </div>

      <div class="min-w-0 @2xl:w-full">
        <CacheCountdown :row="row" class="text-[13px]" />
        <CacheBar :row="row" class="mt-1 w-28 max-w-full @2xl:w-full" />
      </div>

      <div class="min-w-0" :class="row.plan ? 'col-span-2 @2xl:col-span-1' : 'justify-self-end'">
        <PlanStatus v-if="row.plan" :row="row" compact />
        <span v-else-if="unreachable" class="text-xs text-ink-2" :title="noReachWhy(row.s)">Can't be pinged</span>
        <button v-else type="button" class="btn-soft" :aria-expanded="panelOpen" @click="togglePanel">
          <AppIcon name="lucide:flame" /> Keep warm
        </button>
      </div>
    </div>
    <div v-if="panelOpen" class="border-t border-dashed border-line bg-surface-2/50 px-3.5 py-3">
      <div class="ml-auto max-w-lg"><PlanPanel :row="row" @done="ui.planFor = null" @cancel="ui.planFor = null" /></div>
    </div>
  </li>
</template>
