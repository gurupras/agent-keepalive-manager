<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import HarnessIcon from './HarnessIcon.vue'
import SlotButtons from './SlotButtons.vue'
import { noReachWhy, type Row } from '../lib/derive'
import { baseName } from '../lib/format'
import { ui } from '../composables/ui'

// The first line of a session in a list: harness, title (opens the details),
// project and folder chips, then the add-on's session items.
defineProps<{ row: Row, showProject?: boolean }>()
</script>

<template>
  <div class="flex min-w-0 items-center gap-2">
    <HarnessIcon :harness="row.s.harness" class="text-base" />
    <button
      type="button"
      class="min-w-0 cursor-pointer truncate rounded text-left font-medium text-ink outline-none hover:underline focus-visible:ring-2 focus-visible:ring-accent"
      :title="row.s.cwd"
      @click="ui.drawer = row.key"
    >{{ row.title }}</button>
    <span v-if="showProject" class="chip max-w-[40%]" :title="row.s.project"><AppIcon name="lucide:folder" /><span class="truncate">{{ baseName(row.s.project || row.s.cwd) }}</span></span>
    <span v-if="row.where !== '.'" class="chip mono max-w-[30%]" :title="row.s.cwd"><span class="truncate">{{ row.where }}</span></span>
    <AppIcon v-if="row.s.reach === 'none'" name="lucide:wifi-off" class="shrink-0 text-ink-2" :label="'Cannot be pinged: ' + noReachWhy(row.s)" />
    <SlotButtons name="session-actions" :row="row" compact class="ml-auto" />
  </div>
</template>
