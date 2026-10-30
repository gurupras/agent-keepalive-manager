<script setup lang="ts">
import { computed } from 'vue'
import type { Row } from '../lib/derive'

const props = defineProps<{ row: Row }>()

// The fill is a scaleX transform set through the CSSOM (CSP allows that;
// only style attributes in markup are blocked).
const frac = computed(() => props.row.working && !props.row.quiet ? 1 : Math.max(0, Math.min(1, props.row.leftMs / props.row.ttlMs)))
const color = computed(() => {
  const r = props.row
  if (r.working && !r.quiet) return 'bg-info'
  if (r.kept) return 'bg-warm'
  return r.leftMs < 10 * 60_000 ? 'bg-warn' : 'bg-ink-3'
})
</script>

<template>
  <div class="h-1 w-full overflow-hidden rounded-full bg-surface-3" aria-hidden="true">
    <div class="h-full w-full origin-left rounded-full transition-transform duration-1000 ease-linear" :class="color" :style="{ transform: `scaleX(${frac})` }"></div>
  </div>
</template>
