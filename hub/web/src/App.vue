<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import * as api from './api.ts'
import type { BreakdownRow, Dimension, Experiment, Installs, Metric, Point, Summary } from './api.ts'
import { count, money, percent, shortDay } from './format.ts'
import { useRange } from './range.ts'
import { cycleTheme, theme } from './theme.ts'
import BreakdownTable from './components/BreakdownTable.vue'
import ExperimentCard from './components/ExperimentCard.vue'
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
const installs = ref<Installs>('established')
const summary = ref<Summary | null>(null)
const points = ref<Point[] | null>(null)
const tables = ref<Record<Dimension, BreakdownRow[] | null>>(emptyTables())
const experiments = ref<Experiment[] | null>(null)
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
    const p = await api.series(range.value, installs.value, metric.value, signal)
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
  experiments.value = null
  api.summary(range.value, installs.value, signal).then(s => {
    if (signal.aborted) return
    summary.value = s
    needToken.value = false
  }, e => failed(e, signal))
  loadSeries(signal)
  for (const d of DIMENSIONS) {
    api.breakdown(range.value, installs.value, d.key, signal).then(rows => {
      if (!signal.aborted) tables.value[d.key] = rows
    }, e => failed(e, signal))
  }
  api.experiments(range.value, installs.value, signal).then(list => {
    if (!signal.aborted) experiments.value = list
  }, e => failed(e, signal))
}

watch([range, installs], loadAll, { immediate: true })

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

// The hub's setting, known once a summary has loaded; 0 or 1 means every
// install counts, so there is nothing to switch.
const establishedDays = ref(0)
watch(summary, s => { if (s) establishedDays.value = s.establishedDays })

const perInstall = computed(() => {
  const s = summary.value
  return s && s.installs > 0 ? s.net / s.installs : 0
})

const installsHint = computed(() => {
  const s = summary.value
  if (!s) return undefined
  if (s.pending > 0) return `+${count(s.pending)} new, not counted until ${s.establishedDays} reports`
  return 'sent a report in the range'
})

const trimmedHint = computed(() => {
  const s = summary.value
  if (!s) return undefined
  if (s.trimmed === 0) return 'nothing left out below 100 installs'
  return `${count(s.trimmed)} install${s.trimmed === 1 ? '' : 's'} furthest from zero left out`
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
        <div class="flex flex-wrap items-center gap-2">
          <RangePicker :selection="selection" :range="range" @select="select" />
          <div
            v-if="establishedDays > 1" class="flex rounded-lg border border-line bg-surface-1 p-0.5" role="group"
            aria-label="Installs counted"
          >
            <button
              v-for="o in ([['established', `Established (${establishedDays}+ reports)`], ['all', 'All installs']] as const)"
              :key="o[0]" type="button"
              class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
              :class="installs === o[0] ? 'bg-accent text-on-accent' : 'text-text-2 hover:text-text-0'"
              :aria-pressed="installs === o[0]" @click="installs = o[0]"
            >
              {{ o[1] }}
            </button>
          </div>
        </div>
        <p class="text-sm text-text-3 tabular">{{ shortDay(range.from) }} – {{ shortDay(range.to) }} (UTC)</p>
      </div>

      <p v-if="error" class="rounded-lg border border-danger px-4 py-3 text-sm text-danger" role="alert">
        Could not load the dashboard: {{ error }}
      </p>

      <section class="grid grid-cols-2 gap-3 md:grid-cols-4" aria-label="Totals">
        <KpiTile label="Installs" :value="summary ? count(summary.installs) : '–'" :hint="installsHint" />
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
        <KpiTile label="Net per install" :value="summary ? money(perInstall) : '–'" hint="mean over installs in the range" />
        <KpiTile label="Median install" :value="summary ? money(summary.medianNet) : '–'" hint="half the installs netted more" />
        <KpiTile label="Net without outliers" :value="summary ? money(summary.trimmedNet) : '–'" :hint="trimmedHint" />
        <KpiTile
          label="Largest install" :value="summary ? percent(summary.topShare) : '–'"
          :tone="summary && summary.topShare > 0.5 && summary.installs > 2 ? 'danger' : undefined"
          hint="its share of all money moved"
        />
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

      <section class="space-y-3" aria-labelledby="experiments-title">
        <div>
          <h2 id="experiments-title" class="text-sm font-semibold text-text-0">Experiments</h2>
          <p class="text-xs text-text-3">
            Pinging a busy session that makes no API calls, against holding. An episode is one quiet stretch; its cost is
            the pings sent plus the cache re-write when the session came back. Lower is better.
          </p>
        </div>
        <p v-if="experiments === null" class="text-sm text-text-3">Loading…</p>
        <p v-else-if="experiments.length === 0" class="rounded-xl border border-line bg-surface-1 px-4 py-3 text-sm text-text-3 shadow-card">
          No experiment episodes in this range.
        </p>
        <div v-else class="grid gap-4 lg:grid-cols-2">
          <ExperimentCard v-for="e in experiments" :key="e.experiment" :experiment="e" />
        </div>
      </section>
    </main>
  </div>
</template>
