<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import AppMenu from './AppMenu.vue'
import LinkStatus from './LinkStatus.vue'
import type { IconName } from '../icons/names'
import { store } from '../api/store'

const tabs = computed(() => {
  const t: { to: string, label: string, icon: IconName }[] = [
    { to: '/', label: 'Sessions', icon: 'lucide:layout-list' },
    { to: '/ledger', label: 'Ledger', icon: 'lucide:receipt' },
  ]
  if (store.settings?.showMissed) t.push({ to: '/missed', label: 'Missed', icon: 'lucide:lightbulb' })
  return t
})
</script>

<template>
  <header class="@container sticky top-0 z-20 border-b border-line bg-base/95 backdrop-blur">
    <div class="mx-auto flex max-w-6xl flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 @lg:px-5">
      <div class="flex items-center gap-1.5">
        <AppIcon name="lucide:heart-pulse" class="text-lg text-accent" />
        <span class="font-semibold text-ink">keepalive</span>
      </div>
      <nav aria-label="Views" class="order-last -mx-1 flex w-full gap-0.5 @md:order-none @md:mx-0 @md:w-auto">
        <RouterLink
          v-for="t in tabs" :key="t.to" :to="t.to"
          class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] text-ink-2 outline-none hover:bg-surface-2 hover:text-ink focus-visible:ring-2 focus-visible:ring-accent"
          exact-active-class="bg-surface !text-ink font-medium shadow-sm ring-1 ring-line"
        >
          <AppIcon :name="t.icon" /> {{ t.label }}
        </RouterLink>
      </nav>
      <div class="ml-auto flex items-center gap-2">
        <LinkStatus />
        <AppMenu />
      </div>
    </div>
  </header>
</template>
