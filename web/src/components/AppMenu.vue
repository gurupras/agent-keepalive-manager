<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, useTemplateRef } from 'vue'
import AppIcon from './AppIcon.vue'
import type { IconName } from '../icons/names'
import { checkForUpdate, quit, store } from '../api/store'
import { ui } from '../composables/ui'
import { undismissVersion } from '../composables/updates'

const id = useId()
const open = ref(false)
const checking = ref(false)
const confirmQuit = ref(false)
const root = useTemplateRef<HTMLElement>('root')
const trigger = useTemplateRef<HTMLButtonElement>('trigger')

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

function onDoc (e: MouseEvent) {
  if (!root.value?.contains(e.target as Node)) close()
}

async function toggle () {
  if (open.value) return close()
  open.value = true
  document.addEventListener('mousedown', onDoc)
  await nextTick()
  root.value?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
}

function close (refocus = false) {
  open.value = false
  confirmQuit.value = false
  document.removeEventListener('mousedown', onDoc)
  if (refocus) trigger.value?.focus()
}

function select (it: Item) {
  it.run()
  // Quit asks for a second click; the update check reports through a toast.
  if (it.id === 'quit' && confirmQuit.value) return
  close(it.id === 'update')
}

function onKey (e: KeyboardEvent) {
  if (!open.value) return
  const list = [...(root.value?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? [])]
  const i = list.indexOf(document.activeElement as HTMLElement)
  if (e.key === 'ArrowDown') list[(i + 1) % list.length]?.focus()
  else if (e.key === 'ArrowUp') list[(i - 1 + list.length) % list.length]?.focus()
  else if (e.key === 'Home') list[0]?.focus()
  else if (e.key === 'End') list.at(-1)?.focus()
  else if (e.key === 'Escape' || e.key === 'Tab') {
    if (e.key === 'Escape') e.stopPropagation()
    close(e.key === 'Escape')
    return
  } else return
  e.preventDefault()
}

onBeforeUnmount(() => close())
</script>

<template>
  <div ref="root" class="relative" @keydown="onKey">
    <button ref="trigger" type="button" class="icon-btn" aria-label="Menu" aria-haspopup="menu" :aria-expanded="open" :aria-controls="id" @click="toggle">
      <AppIcon name="lucide:ellipsis-vertical" />
    </button>
    <div v-if="open" :id="id" role="menu" aria-label="Menu" class="absolute right-0 z-40 mt-1 w-56 rounded-lg border border-line bg-surface p-1 shadow-pop">
      <template v-for="it in items" :key="it.id">
        <div v-if="it.id === 'quit' && !it.hidden" class="my-1 border-t border-line" role="separator"></div>
        <button
          v-if="!it.hidden" type="button" role="menuitem" :disabled="it.disabled"
          class="flex w-full cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[13px] outline-none hover:bg-surface-2 focus-visible:bg-surface-2 disabled:cursor-wait disabled:opacity-60"
          :class="it.danger ? (confirmQuit ? 'bg-danger-soft font-medium text-danger' : 'text-danger') : 'text-ink-1'"
          @click="select(it)"
        >
          <AppIcon :name="it.icon" :spin="it.id === 'update' && checking" /> {{ it.label }}
        </button>
      </template>
    </div>
  </div>
</template>
