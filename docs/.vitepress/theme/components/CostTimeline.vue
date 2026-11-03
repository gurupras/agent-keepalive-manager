<script setup lang="ts">
import { computed, ref } from 'vue'
import { example, pingCost, rewriteCost, breakEvenPings, intervalMin, warmHours, money } from '../cacheMath'

// What your first prompt back costs, as a function of how long you were away.
const ttlH = example.ttlMin / 60
const ivH = intervalMin / 60
const xMax = 39
const yMax = rewriteCost * 1.08

function cold (t: number) { return t <= ttlH ? pingCost : rewriteCost }
function pingsBy (t: number) { return Math.min(breakEvenPings, Math.floor(t / ivH + 1e-9)) }
// Kept warm: the pings sent so far plus the cache read when you return.
function warm (t: number) { return (pingsBy(t) + 1) * pingCost }

const W = 720, H = 340
const m = { l: 52, r: 20, t: 20, b: 44 }
const x = (t: number) => m.l + (t / xMax) * (W - m.l - m.r)
const y = (v: number) => H - m.b - (v / yMax) * (H - m.t - m.b)

const coldPath = `M${x(0)},${y(pingCost)} H${x(ttlH)} V${y(rewriteCost)} H${x(xMax)}`

const warmPts = computed(() => {
  const pts: [number, number][] = [[0, pingCost]]
  for (let k = 1; k <= breakEvenPings; k++) {
    const t = k * ivH
    pts.push([t, k * pingCost], [t, (k + 1) * pingCost])
  }
  pts.push([warmHours, (breakEvenPings + 1) * pingCost])
  return pts
})
const warmPath = computed(() => warmPts.value.map(([t, v], i) => `${i ? 'L' : 'M'}${x(t)},${y(v)}`).join(' '))
const savedArea = computed(() => {
  const back = [[warmHours, rewriteCost], [ttlH, rewriteCost]]
  const fwd = warmPts.value.filter(([t]) => t >= ttlH)
  return [[ttlH, warm(ttlH)], ...fwd, ...back].map(([t, v], i) => `${i ? 'L' : 'M'}${x(t)},${y(v)}`).join(' ') + ' Z'
})

const xTicks = [0, 6, 12, 18, 24, 30, 36]
const yStep = rewriteCost > 3 ? 1 : 0.5
const yTicks = Array.from({ length: Math.floor(yMax / yStep) + 1 }, (_, i) => i * yStep)

const marks = [
  { t: 1.5, label: 'Back from lunch' },
  { t: 14, label: 'Next morning' },
]

const hover = ref<number | null>(null)
const svg = ref<SVGSVGElement | null>(null)
function onMove (e: PointerEvent) {
  const r = svg.value!.getBoundingClientRect()
  const px = ((e.clientX - r.left) / r.width) * W
  const t = ((px - m.l) / (W - m.l - m.r)) * xMax
  hover.value = t >= 0 && t <= xMax ? t : null
}
const tip = computed(() => {
  const t = hover.value
  if (t == null) return null
  const c = cold(t)
  const planned = t <= warmHours
  const w = planned ? warm(t) : breakEvenPings * pingCost + rewriteCost
  const away = t < 1 ? `${Math.round(t * 60)} min` : `${t.toFixed(1)} h`
  return {
    left: (x(t) / W) * 100,
    away,
    cold: money(c),
    warm: money(w),
    planned,
    saved: planned ? money(Math.max(0, c - w)) : null,
  }
})
</script>

<template>
  <figure class="cost-timeline">
    <figcaption class="title">
      <strong>What your first prompt back costs</strong>
      <span>by how long you were away ({{ example.model }}, {{ example.contextTokens / 1000 }}k-token context, 1-hour cache)</span>
    </figcaption>
    <div class="legend" aria-hidden="true">
      <span><i class="sw cold" />Cache left to expire</span>
      <span><i class="sw warm" />Kept warm by agent-keepalive</span>
      <span><i class="sw saved" />Saved</span>
    </div>

    <div class="plot">
      <svg ref="svg" :viewBox="`0 0 ${W} ${H}`" role="img"
        :aria-label="`Coming back after the cache expires costs ${money(rewriteCost)}. Kept warm, it costs ${money(pingCost)} per ${intervalMin} minutes away, so it stays cheaper for ${Math.floor(warmHours)} hours.`"
        @pointermove="onMove" @pointerleave="hover = null">
        <g class="grid">
          <line v-for="v in yTicks" :key="v" :x1="m.l" :x2="W - m.r" :y1="y(v)" :y2="y(v)" />
        </g>
        <g class="axis">
          <text v-for="v in yTicks" :key="v" :x="m.l - 8" :y="y(v) + 4" text-anchor="end">${{ v.toFixed(2) }}</text>
          <text v-for="t in xTicks" :key="t" :x="x(t)" :y="H - m.b + 20" text-anchor="middle">{{ t }} h</text>
          <text :x="(m.l + W - m.r) / 2" :y="H - 4" text-anchor="middle" class="axis-title">time away</text>
        </g>

        <path :d="savedArea" class="saved" />
        <path :d="coldPath" class="line cold" />
        <path :d="warmPath" class="line warm" />

        <g class="cap">
          <line :x1="x(warmHours)" :x2="x(warmHours)" :y1="m.t" :y2="H - m.b" />
          <text :x="x(warmHours) - 6" :y="y(rewriteCost * 0.4)" text-anchor="end">Plan stops here:</text>
          <text :x="x(warmHours) - 6" :y="y(rewriteCost * 0.4) + 16" text-anchor="end">past it, a rebuild is cheaper</text>
        </g>

        <g v-for="mk in marks" :key="mk.t" class="mark">
          <line :x1="x(mk.t)" :x2="x(mk.t)" :y1="y(rewriteCost)" :y2="y(warm(mk.t))" />
          <circle :cx="x(mk.t)" :cy="y(rewriteCost)" r="5" class="dot cold" />
          <circle :cx="x(mk.t)" :cy="y(warm(mk.t))" r="5" class="dot warm" />
          <text :x="x(mk.t) + 9" :y="y(rewriteCost) + 22">{{ mk.label }}</text>
          <text :x="x(mk.t) + 9" :y="y(rewriteCost) + 39" class="sub">{{ money(warm(mk.t)) }} instead of {{ money(rewriteCost) }}</text>
        </g>

        <text :x="x(xMax) - 4" :y="y(rewriteCost) - 10" text-anchor="end" class="direct">Left to expire: {{ money(rewriteCost) }}</text>
        <text :x="x(22) + 8" :y="y(warm(22)) + 24" text-anchor="start" class="direct">Kept warm: {{ money(pingCost) }} per ping</text>

        <g v-if="hover != null" class="cross">
          <line :x1="x(hover)" :x2="x(hover)" :y1="m.t" :y2="H - m.b" />
        </g>
      </svg>

      <div v-if="tip" class="tooltip" :style="{ left: `${tip.left}%` }" :class="{ flip: tip.left > 60 }">
        <div class="tt-head">Back after {{ tip.away }}</div>
        <div><i class="sw cold" />Left to expire <b>{{ tip.cold }}</b></div>
        <div v-if="tip.planned"><i class="sw warm" />Kept warm <b>{{ tip.warm }}</b></div>
        <div v-else class="tt-note">Past the plan's limit: it has stopped, so the cache expired too.</div>
        <div v-if="tip.saved" class="tt-saved">Saved {{ tip.saved }}</div>
      </div>
    </div>
  </figure>
</template>

<style scoped>
.cost-timeline {
  --cold: #d03b2a;
  --warm: #00897a;
  margin: 24px 0;
}
:global(.dark) .cost-timeline {
  --cold: #e8604f;
  --warm: #14a898;
}
.title { display: flex; flex-direction: column; gap: 2px; line-height: 1.4; }
.title strong { font-size: 17px; color: var(--vp-c-text-1); }
.title span { font-size: 13px; color: var(--vp-c-text-2); }
.legend {
  display: flex; flex-wrap: wrap; gap: 6px 18px;
  margin: 12px 0 4px; font-size: 13px; color: var(--vp-c-text-2);
}
.legend span { display: inline-flex; align-items: center; gap: 6px; }
.sw { display: inline-block; width: 12px; height: 12px; border-radius: 3px; flex: none; }
.sw.cold { background: var(--cold); }
.sw.warm { background: var(--warm); }
.sw.saved { background: color-mix(in srgb, var(--warm) 22%, transparent); }
.plot { position: relative; }
svg { width: 100%; height: auto; display: block; touch-action: pan-y; overflow: visible; }
.grid line { stroke: var(--vp-c-divider); stroke-width: 1; }
.axis text { fill: var(--vp-c-text-3); font-size: 12px; font-variant-numeric: tabular-nums; }
.axis .axis-title { fill: var(--vp-c-text-2); }
.line { fill: none; stroke-width: 2.5; stroke-linejoin: round; }
.line.cold { stroke: var(--cold); }
.line.warm { stroke: var(--warm); }
.saved { fill: color-mix(in srgb, var(--warm) 22%, transparent); }
.cap line { stroke: var(--vp-c-text-3); stroke-dasharray: 4 4; }
.cap text { fill: var(--vp-c-text-2); font-size: 12px; }
.mark line { stroke: var(--vp-c-text-2); stroke-width: 1.5; stroke-dasharray: 2 3; }
.dot { stroke: var(--vp-c-bg); stroke-width: 2; }
.dot.cold { fill: var(--cold); }
.dot.warm { fill: var(--warm); }
.mark text { fill: var(--vp-c-text-1); font-size: 13px; font-weight: 600; }
.mark text.sub { fill: var(--vp-c-text-2); font-weight: 400; }
.direct { fill: var(--vp-c-text-1); font-size: 13px; font-weight: 600; }
.cross line { stroke: var(--vp-c-text-3); stroke-width: 1; }
.tooltip {
  position: absolute; top: 8px; transform: translateX(12px);
  pointer-events: none; min-width: 180px;
  background: var(--vp-c-bg-elv); border: 1px solid var(--vp-c-divider);
  border-radius: 8px; padding: 8px 10px; font-size: 13px; line-height: 1.6;
  box-shadow: var(--vp-shadow-2); color: var(--vp-c-text-2);
}
.tooltip.flip { transform: translateX(calc(-100% - 12px)); }
.tooltip div { display: flex; align-items: center; gap: 6px; }
.tooltip b { margin-left: auto; padding-left: 12px; color: var(--vp-c-text-1); font-variant-numeric: tabular-nums; }
.tt-head { font-weight: 600; color: var(--vp-c-text-1); }
.tt-saved { font-weight: 600; color: var(--vp-c-text-1); border-top: 1px solid var(--vp-c-divider); margin-top: 4px; padding-top: 4px; }
.tt-note { max-width: 200px; font-size: 12px; }
</style>
