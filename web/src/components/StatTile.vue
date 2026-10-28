<script setup lang="ts">
defineProps<{ label: string, value: string, tone?: 'ok' | 'danger' | 'neutral', hint?: string }>()
</script>

<template>
  <div class="min-w-0 rounded-ka border border-line bg-surface px-3 py-2.5" :title="hint">
    <div class="label truncate">{{ label }}</div>
    <div class="mt-0.5 truncate text-lg font-semibold tabular-nums" :class="tone === 'ok' ? 'text-ok' : tone === 'danger' ? 'text-danger' : 'text-ink'">{{ value }}</div>
    <div v-if="$slots.default" class="mt-0.5 truncate text-xs text-ink-2"><slot /></div>
  </div>
</template>
