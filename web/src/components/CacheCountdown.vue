<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { quietText, type Row } from '../lib/derive'
import { countdown, span } from '../lib/format'

const props = defineProps<{ row: Row }>()

/** Below this the countdown turns amber for sessions nobody keeps warm. */
const URGENT_MS = 10 * 60_000

const view = computed(() => {
  const r = props.row
  if (r.stale) {
    return {
      icon: 'lucide:triangle-alert' as const,
      text: 'stale',
      cls: 'text-warn',
      title: 'Reported working for over 2 hours with no new transcript activity. It is treated as idle, and nothing is pinged until the transcript moves again.',
    }
  }
  if (r.working && !r.quiet) return { icon: 'lucide:loader-circle' as const, spin: true, text: 'working', cls: 'text-info', title: 'The agent is working, which keeps its cache fresh.' }
  if (r.leftMs <= 0) {
    return { icon: 'lucide:snowflake' as const, text: 'cold', cls: 'text-cold', title: r.leftMs < -1 ? `The cache expired ${span(-r.leftMs)} ago.` : 'No cached context.' }
  }
  const why = r.quiet ? `Working, but ${quietText(r.quiet)}: the main agent makes no API calls, so its cache cools. ` : ''
  const urgent = !r.kept && r.leftMs < URGENT_MS
  return {
    icon: r.kept ? 'lucide:flame' as const : 'lucide:clock' as const,
    text: countdown(r.leftMs),
    cls: urgent ? 'text-warn font-semibold' : r.kept ? 'text-warm' : 'text-ink',
    title: `${why}The cache expires in ${span(r.leftMs)} unless something refreshes it.`,
  }
})
</script>

<template>
  <span class="inline-flex items-center gap-1 tabular-nums" :class="view.cls" :title="view.title">
    <AppIcon :name="view.icon" :spin="view.spin" class="text-[13px]" />
    <span>{{ view.text }}</span>
    <span class="sr-only">{{ view.title }}</span>
  </span>
</template>
