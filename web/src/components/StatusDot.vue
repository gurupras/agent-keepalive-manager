<script setup lang="ts">
import type { Dot } from '../lib/derive'

// One heat scale: solid for kept warm, faded for warm, cool for cold.
defineProps<{ state: Dot }>()
</script>

<template>
  <span
    class="size-2 shrink-0 rounded-full"
    :class="{ kept: 'bg-warm', warm: 'bg-warm/40 ring-1 ring-warm/70 ring-inset', cold: 'bg-cold/45' }[state]"
    aria-hidden="true"
  ></span>
</template>
