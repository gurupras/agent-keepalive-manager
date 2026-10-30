<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import AppIcon from './AppIcon.vue'
import { loadStartup, store } from '../api/store'

// What the user waits for, in the order it happens: the page reaching the
// server, then the server's own startup (reading transcripts).
const step = computed(() => {
  if (!store.loaded) return 'Connecting to agent-keepalive'
  return store.startup?.step || 'Starting'
})

// The WebSocket brings each step; without one, ask.
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { timer = setInterval(() => { if (store.loaded && store.link !== 'open') loadStartup() }, 1000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <main class="flex min-h-full items-center justify-center p-6">
    <div class="flex flex-col items-center gap-3 text-center" role="status" aria-live="polite">
      <AppIcon name="lucide:loader-circle" spin class="text-3xl text-accent" />
      <p class="text-[13px] text-ink-2">{{ step }}…</p>
    </div>
  </main>
</template>
