<script setup lang="ts">
import { useId, useTemplateRef } from 'vue'
import AppIcon from './AppIcon.vue'
import type { Row } from '../lib/derive'
import { hoursLabel } from '../lib/format'
import { useMenu } from '../composables/menu'
import { usePlanActions } from '../composables/planActions'

const props = defineProps<{ row: Row }>()

const id = useId()
const root = useTemplateRef<HTMLElement>('root')
const { open, toggle, close, onKey } = useMenu(root)
const { busy, run, steps, extendWhy: why } = usePlanActions(() => props.row)

async function pick (h: number) {
  await run('extend', h)
  close()
}
</script>

<template>
  <div ref="root" class="relative inline-block" @keydown="onKey">
    <button
      type="button" class="btn-outline" :aria-expanded="open" aria-haspopup="menu" :aria-controls="id"
      :disabled="!!busy || !steps.length" :title="why"
      @click="toggle"
    >
      <AppIcon :name="busy ? 'lucide:loader-circle' : 'lucide:plus'" :spin="!!busy" /> Extend
    </button>
    <div v-if="open" :id="id" role="menu" class="absolute left-0 z-30 mt-1 w-56 max-w-[calc(100vw-2rem)] rounded-lg border border-line bg-surface p-1 shadow-pop">
      <button
        v-for="h in steps" :key="h" type="button" role="menuitem" class="menu-item text-ink-1"
        @click="pick(h)"
      >Add {{ hoursLabel(h) }}</button>
      <p class="border-t border-line px-2.5 pt-1.5 pb-1 text-[11px] text-ink-2 mt-1">{{ why }}</p>
    </div>
  </div>
</template>
