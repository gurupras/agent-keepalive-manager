<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import type { PingState } from '../api/types'
import type { IconName } from '../icons/names'

const props = defineProps<{ state: PingState, cold?: boolean }>()

interface PingStateInfo { label: string, icon: IconName, cls: string, help: string, spin?: boolean }

const states: Record<PingState, PingStateInfo> = {
  sending: { label: 'Sending', icon: 'lucide:loader-circle', spin: true, cls: 'text-info bg-info-soft', help: 'Recorded; delivery is being attempted.' },
  delivered: { label: 'Delivered', icon: 'lucide:send', cls: 'text-info bg-info-soft', help: 'Handed to the session; waiting for it to appear in the transcript.' },
  seen: { label: 'Seen', icon: 'lucide:eye', cls: 'text-accent-ink bg-accent-soft', help: 'The prompt is in the transcript; waiting for its reply.' },
  verified: { label: 'Verified', icon: 'lucide:circle-check', cls: 'text-ok bg-ok-soft', help: 'The reply was read, so the cache was refreshed.' },
  failed: { label: 'Failed', icon: 'lucide:circle-x', cls: 'text-danger bg-danger-soft', help: 'Delivery failed.' },
  withdrawn: { label: 'Withdrawn', icon: 'lucide:undo-2', cls: 'text-ink-2 bg-surface-2', help: 'Removed from the queue before the session read it.' },
  lost: { label: 'Lost', icon: 'lucide:circle-dashed', cls: 'text-danger bg-danger-soft', help: 'Delivered but never seen in the transcript; the plan was paused.' },
}

const info = computed(() => states[props.state] ?? states.failed)
</script>

<template>
  <span class="inline-flex items-center gap-1">
    <span class="inline-flex items-center gap-1 rounded-md px-1.5 py-px text-[11px] font-medium" :class="info.cls" :title="info.help">
      <AppIcon :name="info.icon" :spin="info.spin" />{{ info.label }}
    </span>
    <span v-if="cold" class="inline-flex items-center gap-1 rounded-md bg-cold-soft px-1.5 py-px text-[11px] font-medium text-cold" title="The reply re-wrote the whole context: the cache had already expired.">
      <AppIcon name="lucide:snowflake" />cold
    </span>
  </span>
</template>
