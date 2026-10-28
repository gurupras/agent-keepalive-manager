<script setup lang="ts">
import type { Inline } from '../lib/markdown'

defineProps<{ parts: Inline[] }>()
</script>

<template>
  <template v-for="(x, i) in parts" :key="i">
    <code v-if="x.t === 'code'" class="rounded bg-surface-2 px-1 mono">{{ x.text }}</code>
    <strong v-else-if="x.t === 'strong'" class="font-semibold text-ink">{{ x.text }}</strong>
    <em v-else-if="x.t === 'em'">{{ x.text }}</em>
    <a v-else-if="x.t === 'link'" :href="x.href" target="_blank" rel="noopener noreferrer" class="break-all text-accent-ink underline underline-offset-2">{{ x.text }}</a>
    <template v-else>{{ x.text }}</template>
  </template>
</template>
