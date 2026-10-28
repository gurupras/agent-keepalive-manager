<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import type { Harness } from '../api/types'
import { dark } from '../composables/theme'

defineProps<{ harness: Harness }>()
</script>

<template>
  <AppIcon
    :name="harness === 'claude' ? 'logos:claude-code' : dark ? 'logos:codex-dark' : 'logos:codex'"
    :label="harness === 'claude' ? 'Claude Code' : 'Codex'"
  />
</template>
