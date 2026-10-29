<script setup lang="ts">
withDefaults(defineProps<{ title: string, subtitle?: string, mono?: boolean }>(), { subtitle: undefined, mono: true })
</script>

<template>
  <div class="flex flex-wrap items-end gap-x-4 gap-y-3">
    <div class="min-w-48 flex-1">
      <h1 class="truncate text-xl font-semibold tracking-tight text-ink" :title="title">{{ title }}</h1>
      <div v-if="subtitle" class="mt-0.5 truncate text-ink-2" :class="mono ? 'mono' : 'text-[13px]'" :title="subtitle">{{ subtitle }}</div>
    </div>
    <slot />
  </div>
</template>
