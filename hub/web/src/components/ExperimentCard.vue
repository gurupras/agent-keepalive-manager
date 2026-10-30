<script setup lang="ts">
import { computed } from 'vue'
import type { Experiment } from '../api.ts'
import { count, decimal, money, percent } from '../format.ts'

const props = defineProps<{ experiment: Experiment }>()

const ARM_LABELS = { hold: 'Hold (control)', ping: 'Ping (treatment)' } as const

// The 95% interval of a mean is 1.96 standard errors either side.
const Z95 = 1.96

// The verdict says which arm is cheaper per episode, but only once the 95%
// interval of the difference excludes zero.
const verdict = computed(() => {
  const d = props.experiment.diff
  if (!d) return { text: 'Not enough episodes yet: each arm needs at least 2.', tone: 'text-text-3' }
  if (!d.significant) {
    return {
      text: `No clear difference yet: ping ${signed(d.meanCostUsd)} per episode (95% CI ${signed(d.ciLowUsd)} to ${signed(d.ciHighUsd)}).`,
      tone: 'text-text-2',
    }
  }
  // Significant: the interval is all on one side of zero, so its bounds
  // read as magnitudes.
  const lo = Math.min(Math.abs(d.ciLowUsd), Math.abs(d.ciHighUsd))
  const hi = Math.max(Math.abs(d.ciLowUsd), Math.abs(d.ciHighUsd))
  const cheaper = d.meanCostUsd < 0
  return {
    text: `Ping costs ${money(Math.abs(d.meanCostUsd))} ${cheaper ? 'less' : 'more'} per episode (95% CI ${money(lo)}–${money(hi)}).`,
    tone: cheaper ? 'text-ok' : 'text-danger',
  }
})

function signed (v: number): string {
  return v > 0 ? `+${money(v)}` : money(v)
}
</script>

<template>
  <section class="rounded-xl border border-line bg-surface-1 shadow-card" :aria-label="`Experiment ${experiment.experiment}`">
    <h3 class="border-b border-line px-4 py-3 font-mono text-sm font-semibold text-text-0">{{ experiment.experiment }}</h3>
    <div class="overflow-x-auto">
      <table class="w-full text-sm tabular">
        <thead>
          <tr class="text-left text-xs text-text-3">
            <th scope="col" class="px-4 py-2 font-medium">Arm</th>
            <th scope="col" class="px-2 py-2 text-right font-medium">Episodes</th>
            <th scope="col" class="px-2 py-2 text-right font-medium">Installs</th>
            <th scope="col" class="px-2 py-2 text-right font-medium">Warm rate</th>
            <th scope="col" class="px-2 py-2 text-right font-medium">Pings / episode</th>
            <th scope="col" class="px-4 py-2 text-right font-medium">Mean cost ± 95%</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in experiment.arms" :key="a.arm" class="border-t border-line">
            <th scope="row" class="px-4 py-2 text-left font-medium text-text-0">{{ ARM_LABELS[a.arm] ?? a.arm }}</th>
            <td class="px-2 py-2 text-right">{{ count(a.episodes) }}</td>
            <td class="px-2 py-2 text-right">{{ count(a.installs) }}</td>
            <td class="px-2 py-2 text-right" :title="`${count(a.warm)} of ${count(a.returned)} returns read the cache`">
              {{ a.returned > 0 ? percent(a.warmRate) : '–' }}
            </td>
            <td class="px-2 py-2 text-right">{{ decimal(a.pingsPerEpisode) }}</td>
            <td class="px-4 py-2 text-right font-medium text-text-0">
              {{ money(a.meanCostUsd) }}
              <span class="font-normal text-text-3">± {{ money(Z95 * a.seCostUsd) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="border-t border-line px-4 py-3 text-sm" :class="verdict.tone">{{ verdict.text }}</p>
  </section>
</template>
