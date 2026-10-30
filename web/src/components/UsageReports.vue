<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
import CopyButton from './CopyButton.vue'
import { previewMetrics, resetInstallId, setExperiments, setMetricsEnabled, store } from '../api/store'
import { errorText } from '../composables/toasts'
import { dateTime, parseTime } from '../lib/format'

// Settings › Usage reports. Shown only when the build has a hub
// (MetricsStatus.available). What is listed under "What is sent" follows
// docs/metrics.md.
const id = useId()
const m = computed(() => store.metrics!)
const busy = ref(false)
const preview = ref<string>()
const previewError = ref('')
const previewBusy = ref(false)
const explainOpen = ref(false)
const confirmReset = ref(false)
const resetBusy = ref(false)

async function reset () {
  if (!confirmReset.value) {
    confirmReset.value = true
    return
  }
  resetBusy.value = true
  await resetInstallId()
  resetBusy.value = false
  confirmReset.value = false
  preview.value = undefined
}

const lockedText = computed(() => m.value.lockedBy === 'admin'
  ? 'Locked by your administrator'
  : m.value.lockedBy === 'profile' ? 'Locked by this build\'s distribution profile' : 'Locked')

async function toggle (e: Event) {
  const on = (e.target as HTMLInputElement).checked
  busy.value = true
  const ok = await setMetricsEnabled(on)
  busy.value = false
  if (!ok) (e.target as HTMLInputElement).checked = m.value.enabled
  if (!on) preview.value = undefined
}

const expBusy = ref(false)
const expLockedText = computed(() => m.value.experimentsLockedBy === 'admin'
  ? 'Locked by your administrator'
  : m.value.experimentsLockedBy === 'profile' ? 'Locked by this build\'s distribution profile' : 'Locked')

async function toggleExperiments (e: Event) {
  const on = (e.target as HTMLInputElement).checked
  expBusy.value = true
  const ok = await setExperiments(on)
  expBusy.value = false
  if (!ok) (e.target as HTMLInputElement).checked = m.value.experiments
}

async function loadPreview () {
  if (preview.value !== undefined) {
    preview.value = undefined
    return
  }
  previewBusy.value = true
  try {
    preview.value = JSON.stringify(await previewMetrics(), null, 2)
    previewError.value = ''
  } catch (err) {
    previewError.value = errorText(err)
  } finally {
    previewBusy.value = false
  }
}

const lastSent = computed(() => {
  const t = parseTime(m.value.lastSent)
  return t ? dateTime(t) : 'Never'
})
</script>

<template>
  <fieldset :aria-describedby="id + '-d'">
    <legend class="mb-2 text-[13px] font-semibold text-ink">Usage reports</legend>
    <p :id="id + '-d'" class="text-xs text-ink-2">
      Once a day, a pseudonymous summary of how much keeping caches warm saved is sent to the hub, so its operators can see totals across installs.
      No names, paths, projects, titles, prompts or transcript content are ever sent.
    </p>

    <label class="mt-3 flex items-start gap-2.5" :class="m.locked ? 'cursor-not-allowed' : 'cursor-pointer'">
      <input
        type="checkbox" class="mt-0.5 size-4 accent-[var(--ka-accent)]" :checked="m.enabled" :disabled="m.locked || busy"
        :aria-describedby="m.locked ? id + '-lock' : undefined" @change="toggle"
      />
      <span>
        <span class="text-[13px] font-medium text-ink">Send usage reports</span>
        <span v-if="m.locked" :id="id + '-lock'" class="mt-0.5 flex items-center gap-1 text-xs text-ink-2">
          <AppIcon name="lucide:lock" /> {{ lockedText }}
        </span>
        <span v-else-if="!m.enabled" class="block text-xs text-ink-2">Off: nothing is sent and nothing is queued.</span>
      </span>
    </label>

    <label class="mt-2.5 flex items-start gap-2.5" :class="m.experimentsLocked ? 'cursor-not-allowed' : 'cursor-pointer'">
      <input
        type="checkbox" class="mt-0.5 size-4 accent-[var(--ka-accent)]" :checked="m.experiments" :disabled="m.experimentsLocked || expBusy"
        :aria-describedby="id + '-e'" @change="toggleExperiments"
      />
      <span>
        <span class="text-[13px] font-medium text-ink">Take part in experiments</span>
        <span :id="id + '-e'" class="block text-xs text-ink-2">
          Helps decide what future versions do. While a session with a keepalive plan is busy but its main agent makes no calls
          (it waits on sub-agents, a long tool or your answer), some of those stretches are pinged and some are not, and the
          summed costs go into the usage report. Treatment pings are capped at 20 and $1 a day, never exceed the break-even of
          a stretch, and stop on their own if they cost more than they save.
          <template v-if="m.experiments && !m.enabled"> Nothing runs while usage reports are off.</template>
        </span>
        <span v-if="m.experimentsLocked" class="mt-0.5 flex items-center gap-1 text-xs text-ink-2">
          <AppIcon name="lucide:lock" /> {{ expLockedText }}
        </span>
      </span>
    </label>

    <dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[13px]">
      <dt class="text-ink-2">Hub</dt>
      <dd class="mono truncate text-ink" :title="m.endpoint">{{ m.endpoint || '—' }}</dd>
      <dt class="text-ink-2">Install id</dt>
      <dd class="flex min-w-0 flex-wrap items-center gap-2">
        <span class="mono min-w-0 truncate text-ink" :title="m.installId">{{ m.installId || '—' }}</span>
        <button type="button" class="btn-outline px-2 py-0.5 text-xs" :class="confirmReset ? 'text-danger' : ''" :disabled="resetBusy" @click="reset" @blur="confirmReset = false">
          <AppIcon :name="resetBusy ? 'lucide:loader-circle' : 'lucide:refresh-cw'" :spin="resetBusy" />
          {{ confirmReset ? 'Click again to reset' : 'Reset id' }}
        </button>
      </dd>
      <dt class="text-ink-2">Last sent</dt>
      <dd class="text-ink">{{ lastSent }}</dd>
      <dt class="text-ink-2">Queued</dt>
      <dd class="text-ink tabular-nums">{{ m.queued }} report{{ m.queued === 1 ? '' : 's' }}</dd>
      <template v-if="m.lastError">
        <dt class="text-ink-2">Last error</dt>
        <dd class="text-danger">{{ m.lastError }}</dd>
      </template>
    </dl>
    <p class="mt-1.5 text-xs text-ink-2">
      The install id is random, made on this machine and tied to nothing about you. Reset it here or with
      <code class="rounded bg-surface-2 px-1 mono">agent-keepalive metrics reset-id</code>; later reports carry the new id.
    </p>

    <div class="mt-3 flex flex-wrap gap-2">
      <button type="button" class="btn-outline" :aria-expanded="preview !== undefined" :aria-controls="id + '-p'" :disabled="previewBusy" @click="loadPreview">
        <AppIcon :name="previewBusy ? 'lucide:loader-circle' : 'lucide:file-json'" :spin="previewBusy" />
        {{ preview !== undefined ? 'Hide preview' : 'Preview next report' }}
      </button>
      <button type="button" class="btn-ghost" :aria-expanded="explainOpen" :aria-controls="id + '-x'" @click="explainOpen = !explainOpen">
        <AppIcon :name="explainOpen ? 'lucide:chevron-down' : 'lucide:chevron-right'" /> What is sent
      </button>
    </div>
    <p v-if="previewError" class="mt-2 text-xs text-danger" role="alert">{{ previewError }}</p>
    <div v-if="preview !== undefined" :id="id + '-p'" class="mt-2 rounded-md border border-line bg-base">
      <div class="flex items-center justify-between gap-2 border-b border-line px-2.5 py-1.5">
        <span class="text-xs text-ink-2">The next report, exactly as it would be sent</span>
        <CopyButton :text="preview" />
      </div>
      <pre class="max-h-64 overflow-auto p-2.5 mono text-ink-1" tabindex="0" aria-label="Report preview">{{ preview }}</pre>
    </div>

    <div v-if="explainOpen" :id="id + '-x'" class="mt-2 space-y-2 rounded-md border border-line bg-base p-3 text-xs text-ink-1">
      <p>One report per UTC day, sent after the day ends (and at start-up for missed days, up to 30 back). Unsent reports wait in the data directory and are retried. A report holds only:</p>
      <ul class="list-disc space-y-0.5 pl-4">
        <li><strong class="text-ink">installId</strong>: the random id above, so the hub can count installs and drop a day sent twice.</li>
        <li><strong class="text-ink">day</strong>: the UTC date counted. No other time is sent.</li>
        <li><strong class="text-ink">version, os, arch, profile, installKind</strong>: the build and how it was installed.</li>
        <li><strong class="text-ink">sessions</strong>: how many Claude Code and Codex sessions ran that day.</li>
        <li><strong class="text-ink">plans</strong>: plans started, how many were indefinite, hours requested.</li>
        <li><strong class="text-ink">pings</strong>: pings sent, verified, cold, failed, and their cost.</li>
        <li><strong class="text-ink">outcomes</strong>: ledger results summed per harness, model, outcome and reason: counts, ping cost, avoided cost, net, context tokens, idle minutes.</li>
        <li><strong class="text-ink">experiments</strong>: when taking part, experiment episodes summed per experiment, arm, harness and model: counts, pings, costs, context tokens, quiet minutes.</li>
      </ul>
      <p><strong class="text-ink">Never sent:</strong> names, user names, e-mail, host names, paths, project names, session titles or ids, prompts, transcript content, model answers, or other settings. The hub does not store the sender's IP address.</p>
      <p>Turning reports off takes effect at once and deletes anything queued. It can also be turned off with <code class="rounded bg-surface-2 px-1 mono">agent-keepalive metrics off</code> or <code class="rounded bg-surface-2 px-1 mono">AGENT_KEEPALIVE_METRICS=off</code>.</p>
    </div>
  </fieldset>
</template>
