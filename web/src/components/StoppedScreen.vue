<script setup lang="ts">
import AppIcon from './AppIcon.vue'

function reload () {
  location.reload()
}
</script>

<template>
  <main class="flex min-h-full items-center justify-center p-6">
    <div class="max-w-sm text-center">
      <AppIcon name="lucide:power" class="text-4xl text-ink-3" />
      <h1 class="mt-3 text-lg font-semibold text-ink">agent-keepalive has stopped</h1>
      <p class="mt-1.5 text-[13px] text-ink-2">
        No sessions are being kept warm. Start it again from your applications menu or with
        <code class="rounded bg-surface-2 px-1 mono">agent-keepalive</code>, then reload this page.
      </p>
      <button type="button" class="btn-outline mt-4" @click="reload"><AppIcon name="lucide:refresh-cw" /> Reload</button>
    </div>
  </main>
</template>
