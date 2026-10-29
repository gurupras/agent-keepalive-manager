<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
import HarnessIcon from './HarnessIcon.vue'
import type { Row } from '../lib/derive'
import { span, tokens } from '../lib/format'
import { ui } from '../composables/ui'

defineProps<{ rows: Row[] }>()
const open = ref(false)
</script>

<template>
  <template v-if="rows.length">
    <button type="button" class="mt-5 flex cursor-pointer items-center gap-2 rounded text-[13px] text-ink-2 outline-none hover:text-ink focus-visible:ring-2 focus-visible:ring-accent" :aria-expanded="open" @click="open = !open">
      <AppIcon :name="open ? 'lucide:chevron-up' : 'lucide:chevron-down'" />
      {{ open ? 'Hide' : 'Show' }} {{ rows.length }} cold session{{ rows.length === 1 ? '' : 's' }}
      <span class="text-ink-3">— their next message re-caches either way</span>
    </button>
    <ul v-if="open" class="card mt-2 divide-y divide-line">
      <li v-for="r in rows" :key="r.key">
        <button type="button" class="flex w-full cursor-pointer items-center gap-3 px-4 py-2.5 text-left text-[13px] text-ink-2 outline-none hover:bg-surface-2/50 focus-visible:bg-surface-2" @click="ui.drawer = r.key">
          <HarnessIcon :harness="r.s.harness" class="opacity-60" />
          <span class="min-w-0 truncate text-ink-1">{{ r.title }}</span>
          <span v-if="r.where !== '.'" class="chip mono"><span class="truncate">{{ r.where }}</span></span>
          <span class="flex-1"></span>
          <span class="shrink-0 tabular-nums">{{ tokens(r.s.contextTokens) }}<template v-if="r.leftMs < -1"> · cold {{ span(-r.leftMs) }}</template></span>
        </button>
      </li>
    </ul>
  </template>
</template>
