<script setup lang="ts">
import { Icon } from '@iconify/vue/offline'
import type { IconName } from '../icons/names'

defineProps<{
  name: IconName
  spin?: boolean
  /** an accessible name; without it the icon is decorative */
  label?: string
}>()
</script>

<template>
  <!-- The wrapper carries classes and the accessible name, and spins: an
       HTML box rotates about its own centre. Spinning the SVG with
       transform-box: fill-box turned it about the bounds of the drawn path
       in WebKit, and the loader's open arc wobbled. -->
  <span
    class="inline-flex size-[1em] shrink-0 items-center justify-center leading-none"
    :class="{ 'animate-spin': spin }"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
    :title="label"
  >
    <Icon :icon="name" class="block size-full" />
  </span>
</template>
