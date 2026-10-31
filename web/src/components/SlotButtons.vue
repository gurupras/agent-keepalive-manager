<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { Row } from '../lib/derive'
import { pickSlot, slotItems, type SlotName } from '../composables/slots'

// The add-on's items in a slot, for one session. `compact` is the form
// used on a list row: icon buttons where the item has an icon.
const props = defineProps<{ name: SlotName, row: Row, compact?: boolean }>()

const items = computed(() => slotItems(props.name, props.row.s))
</script>

<template>
  <div v-if="items.length" class="flex shrink-0 flex-wrap items-center" :class="compact ? 'gap-0.5' : 'gap-2'">
    <template v-for="it in items" :key="it.id">
      <button
        v-if="compact" type="button"
        :class="[it.icon ? 'icon-btn size-6' : 'btn-ghost px-1.5 py-0.5 text-xs', it.danger ? 'text-danger' : 'text-ink-2']"
        :aria-label="it.icon ? it.label : undefined" :title="it.title ?? it.label"
        @click="pickSlot(name, it, row.s)"
      >
        <AppIcon v-if="it.icon" :name="it.icon" /><template v-else>{{ it.label }}</template>
      </button>
      <button
        v-else type="button" :class="it.danger ? 'btn-danger' : 'btn-outline'" :title="it.title"
        @click="pickSlot(name, it, row.s)"
      >
        <AppIcon v-if="it.icon" :name="it.icon" /> {{ it.label }}
      </button>
    </template>
  </div>
</template>
