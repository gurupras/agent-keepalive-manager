<script setup lang="ts">
import { computed, useId, useTemplateRef } from 'vue'
import AppIcon from './AppIcon.vue'
import type { Row } from '../lib/derive'
import { hoursLabel } from '../lib/format'
import { useMenu } from '../composables/menu'
import { type PlanAction, usePlanActions } from '../composables/planActions'
import { pickSlot, slots, type SlotItem } from '../composables/slots'

// The session list's keepalive menu: the drawer's plan controls (Extend,
// Ping now, the framing page's items, Stop) behind a gear.
const props = defineProps<{ row: Row }>()

const id = useId()
const root = useTemplateRef<HTMLElement>('root')
const { open, toggle, close, onKey } = useMenu(root)
const { busy, run, canPing, pingWhy, steps, extendWhy } = usePlanActions(() => props.row)
const p = computed(() => props.row.plan!)

function act (what: PlanAction, hours = 0) {
  close(true)
  run(what, hours)
}

function pick (it: SlotItem) {
  close(true)
  pickSlot('plan-actions', it, props.row.s)
}
</script>

<template>
  <div ref="root" class="relative inline-block" @keydown="onKey">
    <button
      type="button" class="icon-btn" aria-label="Keepalive options" title="Keepalive options"
      aria-haspopup="menu" :aria-expanded="open" :aria-controls="id" :disabled="!!busy"
      @click="toggle"
    >
      <AppIcon :name="busy ? 'lucide:loader-circle' : 'lucide:settings'" :spin="!!busy" />
    </button>
    <div v-if="open" :id="id" role="menu" aria-label="Keepalive options" class="absolute right-0 z-30 mt-1 w-56 rounded-lg border border-line bg-surface p-1 text-left shadow-pop">
      <template v-if="!p.indefinite">
        <div class="px-2.5 pt-1 pb-0.5 text-[11px] font-medium text-ink-2" role="presentation">Extend</div>
        <button v-for="h in steps" :key="h" type="button" role="menuitem" class="menu-item text-ink-1" @click="act('extend', h)">
          <AppIcon name="lucide:plus" /> Add {{ hoursLabel(h) }}
        </button>
        <p v-if="!steps.length" class="px-2.5 pb-1 text-[11px] text-ink-2">{{ extendWhy }}</p>
        <div class="my-1 border-t border-line" role="separator"></div>
      </template>
      <button type="button" role="menuitem" class="menu-item text-ink-1" :disabled="!canPing" :title="pingWhy" @click="act('ping')">
        <AppIcon name="lucide:send" /> Ping now
      </button>
      <button
        v-for="it in slots['plan-actions']" :key="it.id" type="button" role="menuitem" class="menu-item"
        :class="it.danger ? 'text-danger' : 'text-ink-1'" :title="it.title" @click="pick(it)"
      >
        <AppIcon v-if="it.icon" :name="it.icon" /> {{ it.label }}
      </button>
      <div class="my-1 border-t border-line" role="separator"></div>
      <button type="button" role="menuitem" class="menu-item text-danger" @click="act('stop')">
        <AppIcon name="lucide:ban" /> Stop keeping warm
      </button>
    </div>
  </div>
</template>
