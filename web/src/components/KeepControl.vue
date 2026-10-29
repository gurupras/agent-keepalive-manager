<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import PlanStatus from './PlanStatus.vue'
import { noReachWhy, type Row } from '../lib/derive'
import { ui } from '../composables/ui'

// A session's keepalive control in a list: its plan, or the button that
// opens the Keep warm panel (which the list renders under the session).
const props = defineProps<{ row: Row }>()

const open = computed(() => ui.planFor === props.row.key)

function toggle () {
  ui.planFor = open.value ? null : props.row.key
}
</script>

<template>
  <PlanStatus v-if="row.plan" :row="row" compact />
  <span v-else-if="row.s.reach === 'none'" class="text-xs text-ink-2" :title="noReachWhy(row.s)">Can't be pinged</span>
  <button v-else type="button" class="btn-soft" :aria-expanded="open" @click="toggle">
    <AppIcon name="lucide:flame" /> Keep warm<AppIcon :name="open ? 'lucide:chevron-up' : 'lucide:chevron-down'" class="-mr-1 text-xs" />
  </button>
</template>
