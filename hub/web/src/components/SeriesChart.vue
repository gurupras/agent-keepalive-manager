<script setup lang="ts">
// A hand-written SVG line chart: one series, a day per point. It needs no
// library, sets no inline styles (so it runs under style-src 'self'), and
// takes its colours from the palette through CSS classes.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Metric, Point } from '../api.ts'
import { axis, count, money, shortDay } from '../format.ts'

const props = defineProps<{ points: Point[], metric: Metric }>()

const HEIGHT = 240
const PAD = { top: 12, right: 12, bottom: 26, left: 56 }

const box = ref<HTMLElement | null>(null)
const width = ref(640)
let observer: ResizeObserver | undefined
onMounted(() => {
  if (!box.value) return
  width.value = box.value.clientWidth || 640
  observer = new ResizeObserver(entries => {
    const w = entries[0]?.contentRect.width
    if (w) width.value = w
  })
  observer.observe(box.value)
})
onBeforeUnmount(() => observer?.disconnect())

const isMoney = computed(() => props.metric === 'net' || props.metric === 'saved' || props.metric === 'wasted')
const plotW = computed(() => Math.max(1, width.value - PAD.left - PAD.right))
const plotH = HEIGHT - PAD.top - PAD.bottom

/** niceStep is a 1, 2 or 5 times a power of ten near span/ticks. */
function niceStep (span: number, ticks: number): number {
  const raw = span / ticks
  const pow = 10 ** Math.floor(Math.log10(raw))
  const m = raw / pow
  return (m < 1.5 ? 1 : m < 3.5 ? 2 : m < 7.5 ? 5 : 10) * pow
}

const scale = computed(() => {
  const values = props.points.map(p => p.value)
  let lo = Math.min(0, ...values)
  let hi = Math.max(0, ...values)
  if (hi === lo) hi = lo + 1
  const step = niceStep(hi - lo, 4)
  lo = Math.floor(lo / step) * step
  hi = Math.ceil(hi / step) * step
  const ticks: number[] = []
  for (let v = lo; v <= hi + step / 2; v += step) ticks.push(Math.round(v / step) * step)
  return { lo, hi, ticks }
})

function x (i: number): number {
  const n = props.points.length
  return PAD.left + (n <= 1 ? plotW.value / 2 : (i / (n - 1)) * plotW.value)
}
function y (v: number): number {
  const { lo, hi } = scale.value
  return PAD.top + (1 - (v - lo) / (hi - lo)) * plotH
}

const line = computed(() => props.points.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.value).toFixed(1)}`).join(''))
const area = computed(() => {
  const n = props.points.length
  if (!n) return ''
  const base = y(0).toFixed(1)
  return `${line.value}L${x(n - 1).toFixed(1)},${base}L${x(0).toFixed(1)},${base}Z`
})

const xTicks = computed(() => {
  const n = props.points.length
  if (!n) return []
  const want = Math.max(2, Math.floor(plotW.value / 90))
  const every = Math.max(1, Math.ceil(n / want))
  const out: number[] = []
  for (let i = 0; i < n; i += every) out.push(i)
  return out
})

const hover = ref<number | null>(null)
function onMove (e: PointerEvent) {
  const svg = e.currentTarget as SVGSVGElement
  const rect = svg.getBoundingClientRect()
  const px = e.clientX - rect.left
  const n = props.points.length
  if (!n) return
  const i = n <= 1 ? 0 : Math.round(((px - PAD.left) / plotW.value) * (n - 1))
  hover.value = Math.min(n - 1, Math.max(0, i))
}

const focus = computed(() => {
  const i = hover.value ?? props.points.length - 1
  const p = props.points[i]
  return p ? { i, p } : null
})

function fmt (v: number): string {
  return isMoney.value ? money(v) : count(v)
}
</script>

<template>
  <div ref="box" class="w-full">
    <p class="mb-2 h-5 text-sm text-text-2 tabular" aria-live="polite">
      <template v-if="focus">
        <span class="font-medium text-text-0">{{ fmt(focus.p.value) }}</span>
        on {{ shortDay(focus.p.day) }}
      </template>
    </p>
    <svg
      :width="width" :height="HEIGHT" :viewBox="`0 0 ${width} ${HEIGHT}`"
      role="img" :aria-label="`${metric} per day`" class="block touch-none select-none"
      @pointermove="onMove" @pointerleave="hover = null"
    >
      <g>
        <template v-for="t in scale.ticks" :key="t">
          <line :x1="PAD.left" :x2="width - PAD.right" :y1="y(t)" :y2="y(t)" :class="t === 0 ? 'chart-zero' : 'chart-grid'" />
          <text :x="PAD.left - 8" :y="y(t) + 4" text-anchor="end" class="chart-label tabular">{{ axis(t, isMoney) }}</text>
        </template>
        <text
          v-for="i in xTicks" :key="`x${i}`" :x="x(i)" :y="HEIGHT - 6" text-anchor="middle" class="chart-label"
        >{{ shortDay(points[i]!.day) }}</text>
      </g>
      <path v-if="points.length > 1" :d="area" :class="`chart-area tone-${metric}`" />
      <path v-if="points.length > 1" :d="line" :class="`chart-line tone-${metric}`" />
      <g v-if="focus">
        <line v-if="hover !== null" :x1="x(focus.i)" :x2="x(focus.i)" :y1="PAD.top" :y2="PAD.top + plotH" class="chart-cursor" />
        <circle :cx="x(focus.i)" :cy="y(focus.p.value)" r="4" :class="`chart-dot tone-${metric}`" />
      </g>
    </svg>
  </div>
</template>
