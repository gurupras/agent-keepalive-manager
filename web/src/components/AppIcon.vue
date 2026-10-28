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
  <!-- The wrapper carries classes and the accessible name; spinning the SVG
       itself keeps the rotation centred at odd sizes. -->
  <span
    class="inline-flex size-[1em] shrink-0 items-center justify-center leading-none"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
    :title="label"
  >
    <Icon
      :icon="name" class="block size-full"
      :class="{ 'animate-spin origin-center [transform-box:fill-box]': spin }"
    />
  </span>
</template>
