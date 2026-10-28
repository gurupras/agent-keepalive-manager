<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import SessionRow from './SessionRow.vue'
import type { ProjectGroup } from '../lib/derive'

defineProps<{ group: ProjectGroup }>()
</script>

<template>
  <section class="card overflow-visible" :aria-label="group.name">
    <header class="flex items-center gap-2 border-b border-line px-3.5 py-2.5">
      <AppIcon name="lucide:folder" class="text-ink-2" />
      <h2 class="min-w-0 truncate font-semibold text-ink" :title="group.id">{{ group.name }}</h2>
      <span class="ml-auto shrink-0 text-xs text-ink-2">
        <template v-if="group.kept">{{ group.kept }} kept · </template>{{ group.warm }} warm · {{ group.rows.length }} session{{ group.rows.length === 1 ? '' : 's' }}
      </span>
    </header>
    <ul>
      <SessionRow v-for="r in group.rows" :key="r.key" :row="r" />
    </ul>
  </section>
</template>
