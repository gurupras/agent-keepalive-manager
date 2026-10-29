<script setup lang="ts">
import CacheBar from './CacheBar.vue'
import CacheCountdown from './CacheCountdown.vue'
import ContextInfo from './ContextInfo.vue'
import KeepControl from './KeepControl.vue'
import PlanPanel from './PlanPanel.vue'
import SessionTitle from './SessionTitle.vue'
import type { Row } from '../lib/derive'
import { ui } from '../composables/ui'

defineProps<{ rows: Row[], showProject?: boolean }>()

const panelOpen = (r: Row) => ui.planFor === r.key && !r.plan
</script>

<template>
  <!-- Columns need about 48rem; narrower containers (a side panel, a small
       window) get the same rows stacked. -->
  <div class="@container">
    <table class="hidden w-full table-fixed @3xl:table">
      <thead class="text-left text-xs text-ink-2">
        <tr class="border-b border-line">
          <th class="px-5 py-2.5 font-medium">Session</th>
          <th class="w-24 py-2.5 text-right font-medium">Context</th>
          <th class="w-44 py-2.5 pl-6 font-medium">Cache expires in</th>
          <th class="w-64 py-2.5 pr-5 text-right font-medium">Keepalive</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in rows" :key="r.key">
          <tr class="border-t border-line first:border-t-0 hover:bg-surface-2/40">
            <td class="max-w-0 px-5 py-3">
              <SessionTitle :row="r" :show-project="showProject" />
              <div class="mt-0.5 truncate text-[13px] text-ink-2" :title="r.s.summary">{{ r.s.summary || 'No summary yet' }}</div>
            </td>
            <td class="py-3 text-right text-ink"><ContextInfo :s="r.s" align="right" /></td>
            <td class="py-3 pr-4 pl-6">
              <CacheCountdown :row="r" class="text-[13px]" />
              <CacheBar :row="r" class="mt-1.5" />
            </td>
            <td class="py-3 pr-5 text-right"><KeepControl :row="r" /></td>
          </tr>
          <tr v-if="panelOpen(r)" class="bg-surface-2/40">
            <td colspan="4" class="px-5 pt-1 pb-4">
              <div class="ml-auto max-w-lg"><PlanPanel :row="r" @done="ui.planFor = null" @cancel="ui.planFor = null" /></div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>

    <ul class="@3xl:hidden">
      <li v-for="r in rows" :key="r.key" class="border-t border-line px-3.5 py-3 first:border-t-0">
        <div class="flex min-w-0 items-center gap-2">
          <div class="min-w-0 flex-1"><SessionTitle :row="r" :show-project="showProject" /></div>
          <span class="shrink-0 text-ink"><ContextInfo :s="r.s" align="right" /></span>
        </div>
        <div class="mt-0.5 truncate text-[13px] text-ink-2">{{ r.s.summary || 'No summary yet' }}</div>
        <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-2">
          <div class="w-36">
            <CacheCountdown :row="r" class="text-[13px]" />
            <CacheBar :row="r" class="mt-1" />
          </div>
          <div class="ml-auto min-w-0" :class="r.plan ? 'w-full' : ''"><KeepControl :row="r" /></div>
        </div>
        <div v-if="panelOpen(r)" class="mt-3 rounded-ka border border-dashed border-line p-3">
          <PlanPanel :row="r" @done="ui.planFor = null" @cancel="ui.planFor = null" />
        </div>
      </li>
    </ul>
  </div>
</template>
