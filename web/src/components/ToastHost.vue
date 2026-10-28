<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import UpdateToast from './UpdateToast.vue'
import { dismiss, toasts, type ToastKind } from '../composables/toasts'
import type { IconName } from '../icons/names'

const icon: Record<ToastKind, IconName> = { info: 'lucide:info', success: 'lucide:circle-check', error: 'lucide:circle-alert' }
const tone: Record<ToastKind, string> = { info: 'text-info', success: 'text-ok', error: 'text-danger' }
</script>

<template>
  <!-- Bottom right, like an editor's notifications; full width in a narrow panel. -->
  <div class="pointer-events-none fixed right-0 bottom-0 z-50 flex w-full max-w-[26rem] flex-col items-stretch gap-2 p-2 sm:p-4">
    <div aria-live="polite" aria-relevant="additions" class="flex flex-col gap-2">
      <TransitionGroup name="toast">
        <div
          v-for="t in toasts" :key="t.id"
          class="pointer-events-auto flex items-start gap-2.5 rounded-ka border border-line bg-surface px-3 py-2.5 shadow-pop"
          :role="t.kind === 'error' ? 'alert' : 'status'"
        >
          <AppIcon :name="icon[t.kind]" class="mt-0.5 text-base" :class="tone[t.kind]" />
          <div class="min-w-0 flex-1">
            <div class="text-[13px] font-medium break-words text-ink">{{ t.title }}</div>
            <div v-if="t.body" class="mt-0.5 text-xs break-words text-ink-2">{{ t.body }}</div>
            <button v-if="t.action" type="button" class="btn-soft mt-2 px-2.5 py-1 text-xs" @click="t.action.run(); dismiss(t.id)">{{ t.action.label }}</button>
          </div>
          <button type="button" class="icon-btn -mt-1 -mr-1 size-7" aria-label="Dismiss notification" @click="dismiss(t.id)"><AppIcon name="lucide:x" /></button>
        </div>
      </TransitionGroup>
    </div>
    <UpdateToast />
  </div>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
