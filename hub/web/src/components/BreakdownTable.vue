<script setup lang="ts">
import { computed, ref } from 'vue'
import type { BreakdownRow } from '../api.ts'
import { count, money } from '../format.ts'

const props = defineProps<{ title: string, rows: BreakdownRow[] | null, error?: string }>()

const LIMIT = 8
const expanded = ref(false)
const shown = computed(() => {
  const rows = props.rows ?? []
  return expanded.value ? rows : rows.slice(0, LIMIT)
})
</script>

<template>
  <section class="rounded-xl border border-line bg-surface-1 shadow-card">
    <h3 class="border-b border-line px-4 py-3 text-sm font-semibold text-text-0">By {{ title }}</h3>
    <p v-if="error" class="px-4 py-3 text-sm text-danger">{{ error }}</p>
    <p v-else-if="rows === null" class="px-4 py-3 text-sm text-text-3">Loading…</p>
    <p v-else-if="rows.length === 0" class="px-4 py-3 text-sm text-text-3">No reports in this range.</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full text-sm tabular">
        <thead>
          <tr class="text-left text-xs text-text-3">
            <th class="px-4 py-2 font-medium">{{ title }}</th>
            <th class="px-2 py-2 text-right font-medium">Installs</th>
            <th class="px-2 py-2 text-right font-medium">Saved</th>
            <th class="px-2 py-2 text-right font-medium">Wasted</th>
            <th class="px-2 py-2 text-right font-medium">Net</th>
            <th class="px-4 py-2 text-right font-medium">Pings</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in shown" :key="r.key" class="border-t border-line">
            <td class="max-w-56 truncate px-4 py-2 font-mono text-xs text-text-0" :title="r.key">
              <span v-if="r.key">{{ r.key }}</span>
              <span v-else class="text-text-3 italic">none</span>
            </td>
            <td class="px-2 py-2 text-right">{{ count(r.installs) }}</td>
            <td class="px-2 py-2 text-right text-ok">{{ money(r.saved) }}</td>
            <td class="px-2 py-2 text-right text-danger">{{ money(r.wasted) }}</td>
            <td class="px-2 py-2 text-right font-medium" :class="r.net < 0 ? 'text-danger' : 'text-text-0'">{{ money(r.net) }}</td>
            <td class="px-4 py-2 text-right">{{ count(r.pings) }}</td>
          </tr>
        </tbody>
      </table>
      <button
        v-if="rows.length > LIMIT" type="button"
        class="w-full border-t border-line px-4 py-2 text-xs font-medium text-accent hover:bg-surface-2"
        @click="expanded = !expanded"
      >
        {{ expanded ? 'Show fewer' : `Show all ${rows.length}` }}
      </button>
    </div>
  </section>
</template>
