<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import * as api from './api.ts'
import type { BreakdownRow, Dimension, Metric, Point, Summary } from './api.ts'
import { count, money, percent, shortDay } from './format.ts'
import { useRange } from './range.ts'
import { cycleTheme, theme } from './theme.ts'
import BreakdownTable from './components/BreakdownTable.vue'
import KpiTile from './components/KpiTile.vue'
import RangePicker from './components/RangePicker.vue'
import SeriesChart from './components/SeriesChart.vue'
import TokenPrompt from './components/TokenPrompt.vue'

const { selection, range, select } = useRange()
// Resolved against the page, so it follows --base-path.
const favicon = './favicon.svg'

const METRICS: { key: Metric, label: string }[] = [
  { key: 'net', label: 'Net saved' },
  { key: 'saved', label: 'Saved' },
  { key: 'wasted', label: 'Wasted' },
  { key: 'installs', label: 'Installs' },
  { key: 'pings', label: 'Pings' },
]
const DIMENSIONS: { key: Dimension, label: string }[] = [
  { key: 'model', label: 'model' },
  { key: 'harness', label: 'harness' },
  { key: 'version', label: 'version' },
  { key: 'os', label: 'OS' },
  { key: 'installKind', label: 'install kind' },
  { key: 'profile', label: 'profile' },
]

const metric = ref<Metric>('net')
const summary = ref<Summary | null>(null)
const points = ref<Point[] | null>(null)
const tables = ref<Record<Dimension, BreakdownRow[] | null>>(emptyTables())
const error = ref('')
const needToken = ref(false)
const signedIn = ref(false)

function emptyTables (): Record<Dimension, BreakdownRow[] | null> {
  return { model: null, harness: null, version: null, os: null, installKind: null, profile: null }
}

let controller: AbortController | undefined

function failed (e: unknown, signal: AbortSignal) {
  if (signal.aborted) return
  if (e instanceof api.Unauthorized) {
    needToken.value = true
    return
  }
  error.value = (e as Error).message
}

async function loadSeries (signal: AbortSignal) {
  points.value = null
  try {
    const p = await api.series(range.value, metric.value, signal)
    if (!signal.aborted) points.value = p
  } catch (e) {
    failed(e, signal)
  }
}

function loadAll () {
  controller?.abort()
  controller = new AbortController()
  const { signal } = controller
  error.value = ''
  summary.value = null
  tables.value = emptyTables()
  api.summary(range.value, signal).then(s => {
    if (signal.aborted) return
    summary.value = s
    needToken.value = false
  }, e => failed(e, signal))
  loadSeries(signal)
  for (const d of DIMENSIONS) {
    api.breakdown(range.value, d.key, signal).then(rows => {
      if (!signal.aborted) tables.value[d.key] = rows
    }, e => failed(e, signal))
  }
}

watch(range, loadAll, { immediate: true })

// After a reload a valid cookie still signs the user in; ask so sign-out
// is offered.
api.session().then(s => {
  signedIn.value = s.mode === 'token' && s.authenticated
}, () => {})
watch(metric, () => {
  controller?.abort()
  controller = new AbortController()
  loadSeries(controller.signal)
  // The other panels are already loaded for this range.
})

function onLogin () {
  needToken.value = false
  signedIn.value = true
  loadAll()
}

async function onLogout () {
  await api.logout()
  signedIn.value = false
  needToken.value = true
}

const coldRate = computed(() => {
  const s = summary.value
  return s && s.pings > 0 ? s.cold / s.pings : 0
})

const themeLabel = computed(() => ({ system: 'Theme: system', light: 'Theme: light', dark: 'Theme: dark' })[theme.value])
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-line bg-surface-1">
      <div class="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-6">
        <div class="flex items-center gap-3">
          <img :src="favicon" alt="" class="size-7" />
          <div>
            <h1 class="text-base font-semibold text-text-0">agent-keepalive hub</h1>
            <p class="text-xs text-text-3">Prompt-cache keepalive savings across every reporting install</p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button" class="rounded-md border border-line px-3 py-1.5 text-xs font-medium text-text-2 hover:text-text-0"
            :aria-label="themeLabel" :title="themeLabel" @click="cycleTheme"
          >
            {{ themeLabel }}
          </button>
          <button
            v-if="signedIn" type="button"
            class="rounded-md border border-line px-3 py-1.5 text-xs font-medium text-text-2 hover:text-text-0"
            @click="onLogout"
          >
            Sign out
          </button>
        </div>
      </div>
    </header>

    <TokenPrompt v-if="needToken" @done="onLogin" />

    <main v-else class="mx-auto max-w-7xl space-y-6 px-4 py-6 sm:px-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <RangePicker :selection="selection" :range="range" @select="select" />
        <p class="text-sm text-text-3 tabular">{{ shortDay(range.from) }} – {{ shortDay(range.to) }} (UTC)</p>
      </div>

      <p v-if="error" class="rounded-lg border border-danger px-4 py-3 text-sm text-danger" role="alert">
        Could not load the dashboard: {{ error }}
      </p>

      <section class="grid grid-cols-2 gap-3 md:grid-cols-4" aria-label="Totals">
        <KpiTile label="Installs" :value="summary ? count(summary.installs) : '–'" hint="sent a report in the range" />
        <KpiTile label="Active installs" :value="summary ? count(summary.activeInstalls) : '–'" hint="reported in the last 7 days" />
        <KpiTile
          label="Net saved" :value="summary ? money(summary.net) : '–'"
          :tone="summary && summary.net < 0 ? 'danger' : 'accent'" hint="saved minus wasted"
        />
        <KpiTile label="Saved" :value="summary ? money(summary.saved) : '–'" tone="ok" :hint="summary ? `${count(summary.returns)} warm returns` : undefined" />
        <KpiTile label="Wasted" :value="summary ? money(summary.wasted) : '–'" tone="danger" hint="pings nobody came back for" />
        <KpiTile label="Ping cost" :value="summary ? money(summary.pingCost) : '–'" hint="all keepalive pings" />
        <KpiTile label="Pings" :value="summary ? count(summary.pings) : '–'" />
        <KpiTile label="Cold rate" :value="summary ? percent(coldRate) : '–'" :hint="summary ? `${count(summary.cold)} pings found the cache cold` : undefined" />
      </section>

      <section class="rounded-xl border border-line bg-surface-1 p-4 shadow-card" aria-label="Per day">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-sm font-semibold text-text-0">Per day</h2>
          <div class="flex flex-wrap gap-1" role="group" aria-label="Metric">
            <button
              v-for="m in METRICS" :key="m.key" type="button"
              class="rounded-md px-2.5 py-1 text-xs font-medium"
              :class="metric === m.key ? 'bg-surface-2 text-text-0' : 'text-text-2 hover:text-text-0'"
              :aria-pressed="metric === m.key" @click="metric = m.key"
            >
              {{ m.label }}
            </button>
          </div>
        </div>
        <p v-if="points === null" class="py-24 text-center text-sm text-text-3">Loading…</p>
        <SeriesChart v-else :points="points" :metric="metric" />
      </section>

      <section class="grid gap-4 lg:grid-cols-2" aria-label="Breakdowns">
        <BreakdownTable v-for="d in DIMENSIONS" :key="d.key" :title="d.label" :rows="tables[d.key]" />
      </section>
    </main>
  </div>
</template>
