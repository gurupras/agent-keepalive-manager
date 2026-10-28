<script setup lang="ts">
import { computed } from 'vue'
import MarkdownInline from './MarkdownInline.vue'
import { parseMarkdown } from '../lib/markdown'

const props = defineProps<{ source: string }>()
const blocks = computed(() => parseMarkdown(props.source))
const heading = (level: number) => level === 1 ? 'h3' : level === 2 ? 'h4' : 'h5'
</script>

<template>
  <!-- Rendered from parsed data with ordinary nodes: no HTML from the notes reaches the page. -->
  <div class="space-y-2 text-[13px] leading-relaxed text-ink-1">
    <template v-for="(b, bi) in blocks" :key="bi">
      <pre v-if="b.t === 'pre'" class="overflow-x-auto rounded-md bg-surface-2 p-2 mono">{{ b.text }}</pre>
      <component :is="heading(b.level)" v-else-if="b.t === 'h'" class="font-semibold text-ink">
        <MarkdownInline :parts="b.inl" />
      </component>
      <p v-else-if="b.t === 'p'"><MarkdownInline :parts="b.inl" /></p>
      <component :is="b.t" v-else :class="b.t === 'ul' ? 'list-disc' : 'list-decimal'" class="space-y-0.5 pl-5">
        <li v-for="(item, ii) in b.items" :key="ii"><MarkdownInline :parts="item" /></li>
      </component>
    </template>
  </div>
</template>
