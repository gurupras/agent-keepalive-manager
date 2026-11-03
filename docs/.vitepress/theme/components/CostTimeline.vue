<script setup lang="ts">
import { computed, ref } from 'vue'
import { harnesses, selection, pick, scenario, contextTokens, money, duration } from '../cacheMath'

// What your first prompt back costs, as a function of how long you were away.
const s = scenario

const W = 720, H = 340
const m = { l: 52, r: 20, t: 20, b: 44 }

function niceStep (span: number, target: number, steps: number[]) {
  return steps.find(st => span / st <= target) ?? steps[steps.length - 1]
}
const xMax = computed(() => s.value.breakEvenHours * 1.06)
const yMax = computed(() => s.value.rewriteCost * 1.08)
const xStep = computed(() => niceStep(xMax.value, 7, [0.5, 1, 2, 3, 6, 12, 24]))
const yStep = computed(() => {
  const raw = yMax.value / 5
  const p = 10 ** Math.floor(Math.log10(raw))
  return [1, 2, 2.5, 5, 10].map(k => k * p).find(v => v >= raw)!
})
const xTicks = computed(() => Array.from({ length: Math.floor(xMax.value / xStep.value) + 1 }, (_, i) => i * xStep.value))
const yTicks = computed(() => Array.from({ length: Math.floor(yMax.value / yStep.value + 1e-9) + 1 }, (_, i) => i * yStep.value))

const x = (t: number) => m.l + (t / xMax.value) * (W - m.l - m.r)
const y = (v: number) => H - m.b - (v / yMax.value) * (H - m.t - m.b)
const fmtY = (v: number) => yStep.value < 0.01 ? `$${v.toFixed(3)}` : yStep.value < 1 ? `$${v.toFixed(2)}` : `$${v}`

const ivH = computed(() => s.value.intervalMin / 60)
const cold = (t: number) => t <= s.value.ttlH ? s.value.pingCost : s.value.rewriteCost
const pingsBy = (t: number) => Math.min(s.value.breakEvenPings, Math.floor(t / ivH.value + 1e-9))
// Kept warm: the pings sent so far plus the cache read when you return.
const warm = (t: number) => (pingsBy(t) + 1) * s.value.pingCost

const coldPath = computed(() => `M${x(0)},${y(s.value.pingCost)} H${x(s.value.ttlH)} V${y(s.value.rewriteCost)} H${x(xMax.value)}`)
const warmPts = computed(() => {
  const pts: [number, number][] = [[0, s.value.pingCost]]
  for (let k = 1; k <= s.value.breakEvenPings; k++) {
    const t = k * ivH.value
    pts.push([t, k * s.value.pingCost], [t, (k + 1) * s.value.pingCost])
  }
  pts.push([s.value.breakEvenHours, (s.value.breakEvenPings + 1) * s.value.pingCost])
  return pts
})
const toPath = (pts: number[][]) => pts.map(([t, v], i) => `${i ? 'L' : 'M'}${x(t)},${y(v)}`).join(' ')
const warmPath = computed(() => toPath(warmPts.value))
const savedArea = computed(() => {
  const { ttlH, breakEvenHours, rewriteCost } = s.value
  const fwd = warmPts.value.filter(([t]) => t >= ttlH)
  return toPath([[ttlH, warm(ttlH)], ...fwd, [breakEvenHours, rewriteCost], [ttlH, rewriteCost]]) + ' Z'
})

// Two "you came back" examples that fit inside this plan.
const marks = computed(() => {
  const wh = s.value.breakEvenHours
  const out = [{ t: 1.5, label: 'Back from lunch' }]
  out.push(wh > 16 ? { t: 14, label: 'Next morning' } : { t: Math.min(4, wh * 0.6), label: 'After the afternoon' })
  return out.filter(mk => mk.t > s.value.ttlH && mk.t < wh * 0.9)
})

const hover = ref<number | null>(null)
const svg = ref<SVGSVGElement | null>(null)
function onMove (e: PointerEvent) {
  const r = svg.value!.getBoundingClientRect()
  const px = ((e.clientX - r.left) / r.width) * W
  const t = ((px - m.l) / (W - m.l - m.r)) * xMax.value
  hover.value = t >= 0 && t <= xMax.value ? t : null
}
const tip = computed(() => {
  const t = hover.value
  if (t == null) return null
  const c = cold(t)
  const planned = t <= s.value.breakEvenHours
  const w = warm(t)
  return {
    left: (x(t) / W) * 100,
    away: t < 1 ? `${Math.round(t * 60)} min` : `${t.toFixed(1)} h`,
    cold: money(c),
    warm: money(w),
    planned,
    saved: planned ? money(Math.max(0, c - w)) : null,
  }
})
</script>

<template>
  <figure class="cost-timeline">
    <div class="picker" role="group" aria-label="Harness and model">
      <div class="seg" role="radiogroup" aria-label="Harness">
        <button v-for="h in harnesses" :key="h.id" role="radio" :aria-checked="selection.harness === h.id"
          :class="{ on: selection.harness === h.id }" @click="pick(h.id)">{{ h.name }}</button>
      </div>
      <div class="chips" role="radiogroup" aria-label="Model">
        <button v-for="md in s.harness.models" :key="md.id" role="radio" :aria-checked="s.model.id === md.id"
          :class="{ on: s.model.id === md.id }" @click="pick(s.harness.id, md.id)">{{ md.name }}</button>
      </div>
    </div>

    <figcaption class="title">
      <strong>What your first prompt back costs</strong>
      <span>by how long you were away ({{ s.model.name }}, {{ contextTokens / 1000 }}k-token context, {{ s.harness.cacheLabel }})</span>
    </figcaption>
    <div class="legend" aria-hidden="true">
      <span><i class="sw cold" />Cache left to expire</span>
      <span><i class="sw warm" />Kept warm by agent-keepalive</span>
      <span><i class="sw saved" />Saved</span>
    </div>

    <div class="plot">
      <svg ref="svg" :viewBox="`0 0 ${W} ${H}`" role="img"
        :aria-label="`Coming back after the cache expires costs ${money(s.rewriteCost)}. Kept warm, it costs ${money(s.pingCost)} per ${s.intervalMin} minutes away, so it stays cheaper for ${duration(s.breakEvenHours)}.`"
        @pointermove="onMove" @pointerleave="hover = null">
        <g class="grid">
          <line v-for="v in yTicks" :key="v" :x1="m.l" :x2="W - m.r" :y1="y(v)" :y2="y(v)" />
        </g>
        <g class="axis">
          <text v-for="v in yTicks" :key="v" :x="m.l - 8" :y="y(v) + 4" text-anchor="end">{{ fmtY(v) }}</text>
          <text v-for="t in xTicks" :key="t" :x="x(t)" :y="H - m.b + 20" text-anchor="middle">{{ t }} h</text>
          <text :x="(m.l + W - m.r) / 2" :y="H - 4" text-anchor="middle" class="axis-title">time away</text>
        </g>

        <path :d="savedArea" class="saved" />
        <path :d="coldPath" class="line cold" />
        <path :d="warmPath" class="line warm" />

        <g class="cap">
          <line :x1="x(s.breakEvenHours)" :x2="x(s.breakEvenHours)" :y1="m.t" :y2="H - m.b" />
          <text :x="x(s.breakEvenHours) - 6" :y="y(s.rewriteCost * 0.4)" text-anchor="end">Plan stops here:</text>
          <text :x="x(s.breakEvenHours) - 6" :y="y(s.rewriteCost * 0.4) + 16" text-anchor="end">past it, a rebuild is cheaper</text>
        </g>

        <g v-for="mk in marks" :key="mk.label" class="mark">
          <line :x1="x(mk.t)" :x2="x(mk.t)" :y1="y(s.rewriteCost)" :y2="y(warm(mk.t))" />
          <circle :cx="x(mk.t)" :cy="y(s.rewriteCost)" r="5" class="dot cold" />
          <circle :cx="x(mk.t)" :cy="y(warm(mk.t))" r="5" class="dot warm" />
          <text :x="x(mk.t) + 9" :y="y(s.rewriteCost) + 22">{{ mk.label }}</text>
          <text :x="x(mk.t) + 9" :y="y(s.rewriteCost) + 39" class="sub">{{ money(warm(mk.t)) }} instead of {{ money(s.rewriteCost) }}</text>
        </g>

        <text :x="x(xMax) - 4" :y="y(s.rewriteCost) - 10" text-anchor="end" class="direct">Left to expire: {{ money(s.rewriteCost) }}</text>
        <text :x="x(s.breakEvenHours * 0.6) + 8" :y="y(warm(s.breakEvenHours * 0.6)) + 24" class="direct">Kept warm: {{ money(s.pingCost) }} per ping</text>

        <g v-if="hover != null" class="cross">
          <line :x1="x(hover)" :x2="x(hover)" :y1="m.t" :y2="H - m.b" />
        </g>
      </svg>

      <div v-if="tip" class="tooltip" :style="{ left: `${tip.left}%` }" :class="{ flip: tip.left > 60 }">
        <div class="tt-head">Back after {{ tip.away }}</div>
        <div><i class="sw cold" />Left to expire <b>{{ tip.cold }}</b></div>
        <div v-if="tip.planned"><i class="sw warm" />Kept warm <b>{{ tip.warm }}</b></div>
        <div v-else class="tt-note">Past break-even: the plan has stopped, because more pings would cost more than a rebuild.</div>
        <div v-if="tip.saved" class="tt-saved">Saved {{ tip.saved }}</div>
      </div>
    </div>

    <p class="foot">
      A ping reads the cache at ${{ s.model.read.toFixed(2) }}/M tokens; a rebuild writes it at ${{ s.write.toFixed(2) }}/M,
      so one rebuild costs as much as {{ Math.round(s.ratio) }} pings.
      <template v-if="s.harness.id === 'codex'">
        OpenAI adds no surcharge for cache writes, so coming back cold costs the plain input price.
        Its caching is best effort and its lifetime is not published: 30 minutes is what agent-keepalive observes,
        and a ping that finds the cache cold shortens it.
      </template>
      <template v-else>
        Claude Code on the 5-minute cache pays 1.25× input for a write instead of 2×, and pings every few minutes.
      </template>
      List prices; yours can be overridden in the app.
    </p>
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
.picker {
  display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px;
  margin: 0 0 20px;
}
.seg {
  display: inline-flex; padding: 3px; gap: 2px;
  background: var(--vp-c-default-soft); border-radius: 10px;
}
.seg button {
  padding: 6px 14px; border-radius: 8px; font-size: 14px; font-weight: 600;
  color: var(--vp-c-text-2); transition: background 150ms, color 150ms;
}
.seg button.on { background: var(--vp-c-bg); color: var(--vp-c-text-1); box-shadow: 0 1px 3px rgba(0, 0, 0, 0.12); }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chips button {
  padding: 5px 12px; border-radius: 999px; font-size: 13px;
  border: 1px solid var(--vp-c-divider); color: var(--vp-c-text-2);
  transition: border-color 150ms, color 150ms, background 150ms;
}
.chips button:hover { border-color: var(--vp-c-brand-1); color: var(--vp-c-text-1); }
.chips button.on { border-color: var(--vp-c-brand-1); background: var(--vp-c-brand-soft); color: var(--vp-c-text-1); font-weight: 600; }
.seg button:focus-visible, .chips button:focus-visible { outline: 2px solid var(--vp-c-brand-1); outline-offset: 2px; }
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
.foot { margin: 12px 0 0; font-size: 12.5px; line-height: 1.55; color: var(--vp-c-text-3); }
</style>
