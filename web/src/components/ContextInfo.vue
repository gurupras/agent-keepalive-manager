<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { SessionView } from '../api/types'
import { money, shortModel, tokens } from '../lib/format'

// Context size, with what it costs on hover or focus.
withDefaults(defineProps<{ s: SessionView, align?: 'left' | 'right' }>(), { align: 'left' })
</script>

<template>
  <span class="inline-flex items-center gap-1.5 tabular-nums">
    {{ tokens(s.contextTokens) }}
    <span class="group/tip relative inline-flex text-sm font-normal" tabindex="0" aria-label="Cost details">
      <AppIcon name="lucide:info" class="cursor-help" :class="s.costs.known ? 'text-ink-3 hover:text-ink-2' : 'text-warn'" />
      <span
        role="tooltip"
        class="pointer-events-none absolute bottom-full z-30 mb-2 hidden w-60 rounded-lg bg-ink px-3 py-2 text-left text-xs leading-relaxed font-normal text-surface shadow-pop group-hover/tip:block group-focus/tip:block"
        :class="align === 'left' ? 'left-0' : 'right-0'"
      >
        <span class="flex justify-between gap-3"><span class="opacity-70">Re-cache would cost</span><b>{{ money(s.costs.recache) }}</b></span>
        <span class="flex justify-between gap-3"><span class="opacity-70">Each ping costs</span><b>{{ money(s.costs.ping) }}</b></span>
        <span class="flex justify-between gap-3"><span class="opacity-70">Model</span><span class="truncate">{{ shortModel(s.model) }}</span></span>
        <span v-if="!s.costs.known" class="mt-1 block font-medium">No price known for this model</span>
      </span>
    </span>
  </span>
</template>
