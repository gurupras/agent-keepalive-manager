<script setup lang="ts">
defineProps<{ label: string, value: string, hint?: string, tone?: 'ok' | 'danger' | 'accent' }>()
</script>

<template>
  <div class="rounded-xl border border-line bg-surface-1 p-4 shadow-card">
    <p class="text-xs font-medium tracking-wide text-text-2 uppercase">{{ label }}</p>
    <p
      class="mt-1 text-2xl font-semibold tabular"
      :class="{ 'text-ok': tone === 'ok', 'text-danger': tone === 'danger', 'text-accent': tone === 'accent', 'text-text-0': !tone }"
    >
      {{ value }}
    </p>
    <p v-if="hint" class="mt-0.5 text-xs text-text-3">{{ hint }}</p>
  </div>
</template>
