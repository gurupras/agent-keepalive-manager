<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import AppSidebar from './components/AppSidebar.vue'
import AboutDialog from './components/AboutDialog.vue'
import SessionDrawer from './components/SessionDrawer.vue'
import SettingsDialog from './components/SettingsDialog.vue'
import StoppedScreen from './components/StoppedScreen.vue'
import ToastHost from './components/ToastHost.vue'
import { closeTopmost, searchInput, ui } from './composables/ui'
import { store } from './api/store'

const route = useRoute()
watch(() => route.fullPath, () => { ui.planFor = null; ui.nav = false })

function isTyping (el: Element | null) {
  return el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement ||
    el instanceof HTMLSelectElement || (el instanceof HTMLElement && el.isContentEditable)
}

// "/" finds a project; Escape closes the topmost overlay (dialogs close
// themselves first).
function onKey (e: KeyboardEvent) {
  if (e.key === '/' && !isTyping(document.activeElement) && !e.ctrlKey && !e.metaKey && !e.altKey) {
    e.preventDefault()
    findProject()
  } else if (e.key === 'Escape' && !e.defaultPrevented && closeTopmost()) {
    e.preventDefault()
  }
}

// In a narrow window the search field is in the folded sidebar: open it first.
async function findProject () {
  const el = searchInput.value
  if (!el) return
  if (getComputedStyle(el).visibility === 'hidden') {
    ui.nav = true
    await nextTick()
  }
  el.focus()
  el.select()
}

function reload () {
  location.reload()
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <StoppedScreen v-if="store.quit" />
  <!-- Sized by its container, not the window, so it also folds up inside a
       narrow panel of another page. -->
  <div v-else class="@container/app relative flex h-full overflow-hidden">
    <!-- Below 64rem the sidebar slides over the page, opened from the top bar. -->
    <AppSidebar
      class="@max-5xl/app:absolute @max-5xl/app:inset-y-0 @max-5xl/app:left-0 @max-5xl/app:z-40 @max-5xl/app:max-w-[85%] @max-5xl/app:shadow-pop @max-5xl/app:transition-transform"
      :class="ui.nav ? '' : '@max-5xl/app:invisible @max-5xl/app:-translate-x-full'"
    />
    <div v-if="ui.nav" class="absolute inset-0 z-30 bg-black/40 @5xl/app:hidden" aria-hidden="true" @click="ui.nav = false"></div>
    <main class="min-w-0 flex-1 overflow-y-auto">
      <div class="sticky top-0 z-20 flex items-center gap-2 border-b border-line bg-base px-3 py-2 @5xl/app:hidden">
        <button type="button" class="icon-btn" aria-label="Open navigation" :aria-expanded="ui.nav" @click="ui.nav = true">
          <AppIcon name="lucide:menu" />
        </button>
        <AppIcon name="lucide:heart-pulse" class="text-lg text-accent" />
        <span class="font-semibold text-ink">keepalive</span>
      </div>
      <div v-if="store.authError" class="border-b border-line bg-danger-soft px-3 py-2 text-center text-[13px] text-ink" role="alert">
        agent-keepalive refused this page's access token. It may have been restarted with a new data directory, or this page did not come from it.
        <button type="button" class="ml-1 font-medium text-danger underline underline-offset-2" @click="reload">Reload the page</button>
      </div>
      <div class="mx-auto max-w-6xl px-4 pt-5 pb-24 @xl/app:px-8 @xl/app:pt-7">
        <RouterView />
      </div>
    </main>
    <SessionDrawer />
    <SettingsDialog />
    <AboutDialog />
  </div>
  <ToastHost />
</template>
