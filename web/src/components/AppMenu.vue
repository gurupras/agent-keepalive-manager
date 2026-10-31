<script setup lang="ts">
import { computed, ref, useId, useTemplateRef } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from '../icons/names'
import { checkForUpdate, quit, store } from '../api/store'
import { ui } from '../composables/ui'
import { undismissVersion } from '../composables/updates'
import { useMenu } from '../composables/menu'

const id = useId()
const checking = ref(false)
const confirmQuit = ref(false)
const root = useTemplateRef<HTMLElement>('root')
const { open, toggle, close, onKey } = useMenu(root, () => { confirmQuit.value = false })

interface Item { id: string, label: string, icon: IconName, run: () => void, danger?: boolean, hidden?: boolean, disabled?: boolean }

const items = computed<Item[]>(() => [
  { id: 'settings', label: 'Settings', icon: 'lucide:settings', run: () => { ui.settings = true } },
  {
    id: 'update',
    label: checking.value ? 'Checking for updates…' : 'Check for updates',
    icon: 'lucide:refresh-cw',
    run: check,
    hidden: store.update?.enabled === false,
    disabled: checking.value,
  },
  { id: 'about', label: 'About', icon: 'lucide:info', run: () => { ui.about = true } },
  {
    id: 'quit',
    label: confirmQuit.value ? 'Click again to quit' : 'Quit agent-keepalive',
    icon: 'lucide:power',
    danger: true,
    run: doQuit,
    hidden: store.info?.supervised,
  },
])

async function check () {
  checking.value = true
  const r = await checkForUpdate()
  checking.value = false
  // A manual check shows the toast again even if this version was dismissed.
  if (r?.state === 'available' && r.latest) undismissVersion(r.latest.version)
}

async function doQuit () {
  if (!confirmQuit.value) {
    confirmQuit.value = true
    return
  }
  await quit()
}

function select (it: Item) {
  it.run()
  // Quit asks for a second click; the update check reports through a toast.
  if (it.id === 'quit' && confirmQuit.value) return
  close(it.id === 'update')
}

</script>

<template>
  <div ref="root" class="relative" @keydown="onKey">
    <button type="button" class="icon-btn" aria-label="Menu" aria-haspopup="menu" :aria-expanded="open" :aria-controls="id" @click="toggle">
      <AppIcon name="lucide:ellipsis-vertical" />
    </button>
    <div v-if="open" :id="id" role="menu" aria-label="Menu" class="absolute right-0 z-40 mt-1 w-56 rounded-lg border border-line bg-surface p-1 shadow-pop">
      <template v-for="it in items" :key="it.id">
        <div v-if="it.id === 'quit' && !it.hidden" class="my-1 border-t border-line" role="separator"></div>
        <button
          v-if="!it.hidden" type="button" role="menuitem" :disabled="it.disabled"
          class="menu-item disabled:cursor-wait"
          :class="it.danger ? (confirmQuit ? 'bg-danger-soft font-medium text-danger' : 'text-danger') : 'text-ink-1'"
          @click="select(it)"
        >
          <AppIcon :name="it.icon" :spin="it.id === 'update' && checking" /> {{ it.label }}
        </button>
      </template>
    </div>
  </div>
</template>
