<script setup lang="ts">
import { ref, watch } from 'vue'
import type { DayRange } from '../api.ts'
import { PRESETS, type Selection, utcDay } from '../range.ts'

const props = defineProps<{ selection: Selection, range: DayRange }>()
const emit = defineEmits<{ select: [Selection] }>()

const custom = ref('from' in props.selection)
const from = ref(props.range.from)
const to = ref(props.range.to)
const today = utcDay(new Date())
watch(() => props.range, r => { from.value = r.from; to.value = r.to })
watch(() => props.selection, s => { if ('from' in s) custom.value = true })

function pick (days: number) {
  custom.value = false
  emit('select', { days })
}

function apply () {
  if (from.value && to.value && from.value <= to.value) emit('select', { from: from.value, to: to.value })
}

const btn = 'rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex rounded-lg border border-line bg-surface-1 p-0.5" role="group" aria-label="Date range">
      <button
        v-for="d in PRESETS" :key="d" type="button"
        :class="[btn, !custom && 'days' in selection && selection.days === d ? 'bg-accent text-on-accent' : 'text-text-2 hover:text-text-0']"
        :aria-pressed="!custom && 'days' in selection && selection.days === d"
        @click="pick(d)"
      >
        {{ d }} days
      </button>
      <button
        type="button"
        :class="[btn, custom ? 'bg-accent text-on-accent' : 'text-text-2 hover:text-text-0']"
        :aria-pressed="custom"
        @click="custom = true"
      >
        Custom
      </button>
    </div>
    <form v-if="custom" class="flex flex-wrap items-center gap-2 text-sm" @submit.prevent="apply">
      <label class="sr-only" for="range-from">From</label>
      <input id="range-from" v-model="from" type="date" :max="to || today" required class="rounded-md border border-line bg-surface-1 px-2 py-1 text-text-0" />
      <span class="text-text-3">to</span>
      <label class="sr-only" for="range-to">To</label>
      <input id="range-to" v-model="to" type="date" :min="from" :max="today" required class="rounded-md border border-line bg-surface-1 px-2 py-1 text-text-0" />
      <button type="submit" :class="[btn, 'border border-line bg-surface-1 text-text-0 hover:bg-surface-2']">Apply</button>
    </form>
  </div>
</template>
