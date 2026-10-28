<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import AppHeader from './components/AppHeader.vue'
import AboutDialog from './components/AboutDialog.vue'
import SessionDrawer from './components/SessionDrawer.vue'
import SettingsDialog from './components/SettingsDialog.vue'
import StoppedScreen from './components/StoppedScreen.vue'
import ToastHost from './components/ToastHost.vue'
import { closeTopmost } from './composables/ui'
import { store } from './api/store'

// Dialogs close themselves on Escape; this closes an open Keep warm panel.
function onKey (e: KeyboardEvent) {
  if (e.key === 'Escape' && !e.defaultPrevented && closeTopmost()) e.preventDefault()
}

function reload () {
  location.reload()
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <StoppedScreen v-if="store.quit" />
  <div v-else class="flex min-h-full flex-col">
    <AppHeader />
    <div v-if="store.authError" class="border-b border-line bg-danger-soft px-3 py-2 text-center text-[13px] text-ink" role="alert">
      agent-keepalive refused this page's access token. It may have been restarted with a new data directory, or this page did not come from it.
      <button type="button" class="ml-1 font-medium text-danger underline underline-offset-2" @click="reload">Reload the page</button>
    </div>
    <main class="mx-auto w-full max-w-6xl flex-1 px-3 pt-4 pb-24 @container sm:px-5">
      <RouterView />
    </main>
    <SessionDrawer />
    <SettingsDialog />
    <AboutDialog />
  </div>
  <ToastHost />
</template>
