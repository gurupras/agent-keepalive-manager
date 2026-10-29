<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { viewMode } from '../composables/ui'
</script>

<template>
  <div class="flex rounded-lg bg-surface-2 p-0.5" role="group" aria-label="Layout">
    <button
      type="button" class="btn px-2! py-1!" :class="viewMode === 'cards' ? 'bg-surface text-ink shadow-sm' : 'text-ink-2 hover:text-ink'"
      title="Cards" aria-label="Cards" :aria-pressed="viewMode === 'cards'" @click="viewMode = 'cards'"
    >
      <AppIcon name="lucide:layout-grid" />
    </button>
    <button
      type="button" class="btn px-2! py-1!" :class="viewMode === 'table' ? 'bg-surface text-ink shadow-sm' : 'text-ink-2 hover:text-ink'"
      title="Table" aria-label="Table" :aria-pressed="viewMode === 'table'" @click="viewMode = 'table'"
    >
      <AppIcon name="lucide:rows-3" />
    </button>
  </div>
</template>
