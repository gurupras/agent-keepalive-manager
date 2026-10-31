<script setup lang="ts">
// Pings by the context size they were sent at: one bar per bucket. Bars are
// SVG rects, so the chart sets no inline styles (style-src 'self').
import { computed, ref } from 'vue'
import type { PingContext } from '../api.ts'
import { count, percent } from '../format.ts'

const props = defineProps<{ data: PingContext | null }>()

const ALL = ''
const modelKey = ref(ALL)

const models = computed(() => {
  const totals = new Map<string, number>()
  for (const r of props.data?.rows ?? []) {
    const k = `${r.harness}/${r.model}`
    totals.set(k, (totals.get(k) ?? 0) + r.pings)
  }
  return [...totals.entries()].sort((a, b) => b[1] - a[1]).map(e => e[0])
})

const bars = computed(() => {
  const d = props.data
  if (!d) return []
  const n = Math.floor(d.maxTokens / d.bucketTokens) + 1
  const pings = new Array<number>(n).fill(0)
  for (const r of d.rows) {
    if (modelKey.value !== ALL && `${r.harness}/${r.model}` !== modelKey.value) continue
    pings[Math.min(Math.floor(r.fromTokens / d.bucketTokens), n - 1)]! += r.pings
  }
  const total = pings.reduce((a, b) => a + b, 0)
  // Buckets past the largest one with pings are left out, up to 200k.
  let last = Math.max(Math.floor(200_000 / d.bucketTokens) - 1, 0)
  pings.forEach((p, i) => { if (p > 0) last = Math.max(last, i) })
  const peak = Math.max(...pings, 1)
  return pings.slice(0, last + 1).map((p, i) => {
    const from = i * d.bucketTokens
    const label = from >= d.maxTokens ? `${tokens(from)}+` : `${tokens(from)}–${tokens(from + d.bucketTokens)}`
    return { label, pings: p, share: total > 0 ? p / total : 0, width: p / peak }
  })
})

const total = computed(() => bars.value.reduce((a, b) => a + b.pings, 0))

function tokens (n: number): string {
  return n >= 1_000_000 ? `${n / 1_000_000}M` : `${n / 1000}k`
}
</script>

<template>
  <section class="rounded-xl border border-line bg-surface-1 p-4 shadow-card" aria-labelledby="ping-context-title">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div>
        <h2 id="ping-context-title" class="text-sm font-semibold text-text-0">Context size at ping</h2>
        <p class="text-xs text-text-3">Pings by the session's context, in tokens, when each was sent.</p>
      </div>
      <select
        v-if="models.length > 1" v-model="modelKey" aria-label="Model"
        class="rounded-md border border-line bg-surface-1 px-2 py-1 text-xs text-text-0"
      >
        <option :value="ALL">All models</option>
        <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
      </select>
    </div>
    <p v-if="data === null" class="py-12 text-center text-sm text-text-3">Loading…</p>
    <p v-else-if="total === 0" class="py-12 text-center text-sm text-text-3">No pings with a context size in this range.</p>
    <table v-else class="w-full text-sm tabular">
      <thead class="sr-only">
        <tr><th>Context</th><th>Share</th><th>Pings</th></tr>
      </thead>
      <tbody>
        <tr v-for="b in bars" :key="b.label">
          <td class="w-28 py-0.5 pr-3 font-mono text-xs whitespace-nowrap text-text-2">{{ b.label }}</td>
          <td class="py-0.5">
            <svg class="block h-4 w-full" viewBox="0 0 100 16" preserveAspectRatio="none" aria-hidden="true">
              <rect x="0" y="2" :width="Math.max(b.width * 100, b.pings > 0 ? 0.5 : 0)" height="12" class="chart-bar" />
            </svg>
          </td>
          <td class="w-24 py-0.5 pl-3 text-right text-xs whitespace-nowrap text-text-2">
            {{ count(b.pings) }} <span class="text-text-3">· {{ percent(b.share) }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
