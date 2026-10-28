<script setup lang="ts">
import { nextTick, onBeforeUnmount, useTemplateRef, watch } from 'vue'
import AppIcon from './AppIcon.vue'

// A native modal <dialog>: the browser traps focus, makes the page behind it
// inert and closes it on Escape. `side` makes it a drawer on the right.
const props = withDefaults(defineProps<{
  open: boolean
  title: string
  side?: boolean
  /** Tailwind width class of the panel */
  width?: string
}>(), { side: false, width: 'w-[34rem]' })

const emit = defineEmits<{ close: [] }>()
const dlg = useTemplateRef<HTMLDialogElement>('dlg')
let opener: Element | null = null

watch(() => props.open, async open => {
  await nextTick()
  const d = dlg.value
  if (!d) return
  if (open && !d.open) {
    opener = document.activeElement
    d.showModal()
  } else if (!open && d.open) {
    d.close()
  }
}, { immediate: true })

function onClose () {
  if (props.open) emit('close')
  // Give focus back to what opened the dialog.
  if (opener instanceof HTMLElement && opener.isConnected) opener.focus()
  opener = null
}

// A click on the backdrop lands on the <dialog> itself, outside its panel.
function onClick (e: MouseEvent) {
  if (e.target === dlg.value) emit('close')
}

onBeforeUnmount(() => { if (dlg.value?.open) dlg.value.close() })
</script>

<template>
  <dialog
    ref="dlg"
    class="ka-dialog m-0 max-h-none max-w-none bg-transparent p-0 text-ink backdrop:bg-[rgb(8_12_20/45%)]"
    :class="side ? 'ka-side' : 'ka-center'"
    :aria-label="title"
    @close="onClose"
    @click="onClick"
  >
    <div
      class="flex max-w-full flex-col border-line bg-surface shadow-pop"
      :class="[width, side ? 'h-dvh border-l' : 'max-h-[min(90dvh,48rem)] rounded-ka border']"
    >
      <header class="flex shrink-0 items-center gap-2 border-b border-line px-4 py-3">
        <slot name="icon" />
        <h2 class="min-w-0 flex-1 truncate text-base font-semibold text-ink">{{ title }}</h2>
        <button type="button" class="icon-btn" aria-label="Close" @click="emit('close')"><AppIcon name="lucide:x" /></button>
      </header>
      <div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        <slot />
      </div>
      <footer v-if="$slots.footer" class="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-line px-4 py-3">
        <slot name="footer" />
      </footer>
    </div>
  </dialog>
</template>

<style scoped>
.ka-dialog[open] {
  display: flex;
  inset: 0;
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.ka-center[open] {
  align-items: center;
  justify-content: center;
  padding: 1rem;
}

.ka-side[open] {
  justify-content: flex-end;
}
</style>
