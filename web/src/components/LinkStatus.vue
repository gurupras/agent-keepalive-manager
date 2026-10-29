<script setup lang="ts">
import { computed } from 'vue'
import { store, POLL_MS } from '../api/store'

const link = computed(() => {
  if (store.mock) return { text: 'Mock data', detail: 'Running against the in-browser mock backend.', cls: 'bg-[#8b5cf6]', live: true }
  if (store.link === 'open') return { text: 'Live', detail: 'Connected: updates arrive as they happen.', cls: 'bg-ok', live: true }
  if (store.link === 'connecting') return { text: 'Connecting…', detail: 'Opening the live connection.', cls: 'bg-warn', live: false }
  return {
    text: 'Reconnecting',
    detail: `The live connection is down. Retrying with backoff; sessions refresh every ${POLL_MS / 1000} s meanwhile.` +
      (store.error ? ` Last error: ${store.error}` : ''),
    cls: 'bg-danger',
    live: false,
  }
})
</script>

<template>
  <span class="inline-flex items-center gap-1.5 text-xs text-ink-2" :title="link.detail" role="status">
    <span class="relative flex size-2">
      <span v-if="!link.live" class="absolute inline-flex size-full animate-ping rounded-full opacity-60" :class="link.cls"></span>
      <span class="relative inline-flex size-2 rounded-full" :class="link.cls"></span>
    </span>
    <span>{{ link.text }}</span>
  </span>
</template>
