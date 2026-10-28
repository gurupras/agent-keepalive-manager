<script setup lang="ts">
import { computed, reactive, ref, useId, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import ModalDialog from './ModalDialog.vue'
import UsageReports from './UsageReports.vue'
import { saveSettings, store } from '../api/store'
import { ui } from '../composables/ui'
import { themePref, type ThemePref } from '../composables/theme'
import type { Settings } from '../api/types'

const id = useId()
const form = reactive<Settings>({ maxHours: 36, marginMin: 5, scanSeconds: 5, showMissed: false })
const busy = ref(false)

// Load the current settings each time the dialog opens.
watch(() => ui.settings, open => { if (open && store.settings) Object.assign(form, store.settings) }, { immediate: true })

const limits = {
  maxHours: { min: 0.5, max: 168, step: 0.5 },
  marginMin: { min: 1, max: 15, step: 1 },
  scanSeconds: { min: 2, max: 60, step: 1 },
} as const

const errors = computed(() => {
  const e: Partial<Record<keyof typeof limits, string>> = {}
  for (const k of Object.keys(limits) as (keyof typeof limits)[]) {
    const v = form[k]
    const l = limits[k]
    if (typeof v !== 'number' || Number.isNaN(v) || v < l.min || v > l.max) e[k] = `Between ${l.min} and ${l.max}.`
  }
  return e
})
const valid = computed(() => !Object.keys(errors.value).length)
const dirty = computed(() => !!store.settings && (Object.keys(form) as (keyof Settings)[]).some(k => form[k] !== store.settings![k]))

async function save () {
  if (!valid.value) return
  busy.value = true
  const ok = await saveSettings({ ...form })
  busy.value = false
  if (ok) ui.settings = false
}

const themes: { id: ThemePref, label: string }[] = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' },
]
</script>

<template>
  <ModalDialog :open="ui.settings" title="Settings" @close="ui.settings = false">
    <template #icon><AppIcon name="lucide:settings" class="text-ink-2" /></template>
    <form :id="id" class="space-y-6" novalidate @submit.prevent="save">
      <fieldset class="space-y-4">
        <legend class="mb-2 text-[13px] font-semibold text-ink">Keepalive</legend>
        <div class="grid gap-1 sm:grid-cols-[1fr_8rem] sm:items-start sm:gap-x-4">
          <div>
            <label :for="id + '-max'" class="text-[13px] font-medium text-ink">Longest plan (hours)</label>
            <p :id="id + '-max-h'" class="text-xs text-ink-2">No plan runs longer than this. Plans are also capped at each model's break-even, where pings start to cost more than a re-cache.</p>
          </div>
          <div>
            <input :id="id + '-max'" v-model.number="form.maxHours" type="number" class="input tabular-nums" v-bind="limits.maxHours" :aria-invalid="!!errors.maxHours" :aria-describedby="id + '-max-h ' + id + '-max-e'" />
            <p :id="id + '-max-e'" class="mt-0.5 text-xs text-danger">{{ errors.maxHours }}</p>
          </div>
        </div>
        <div class="grid gap-1 sm:grid-cols-[1fr_8rem] sm:items-start sm:gap-x-4">
          <div>
            <label :for="id + '-margin'" class="text-[13px] font-medium text-ink">Safety margin (minutes)</label>
            <p :id="id + '-margin-h'" class="text-xs text-ink-2">Ping this long before the cache would expire.</p>
          </div>
          <div>
            <input :id="id + '-margin'" v-model.number="form.marginMin" type="number" class="input tabular-nums" v-bind="limits.marginMin" :aria-invalid="!!errors.marginMin" :aria-describedby="id + '-margin-h ' + id + '-margin-e'" />
            <p :id="id + '-margin-e'" class="mt-0.5 text-xs text-danger">{{ errors.marginMin }}</p>
          </div>
        </div>
        <div class="grid gap-1 sm:grid-cols-[1fr_8rem] sm:items-start sm:gap-x-4">
          <div>
            <label :for="id + '-scan'" class="text-[13px] font-medium text-ink">Scan every (seconds)</label>
            <p :id="id + '-scan-h'" class="text-xs text-ink-2">How often sessions and transcripts are read.</p>
          </div>
          <div>
            <input :id="id + '-scan'" v-model.number="form.scanSeconds" type="number" class="input tabular-nums" v-bind="limits.scanSeconds" :aria-invalid="!!errors.scanSeconds" :aria-describedby="id + '-scan-h ' + id + '-scan-e'" />
            <p :id="id + '-scan-e'" class="mt-0.5 text-xs text-danger">{{ errors.scanSeconds }}</p>
          </div>
        </div>
        <label class="flex cursor-pointer items-start gap-2.5">
          <input v-model="form.showMissed" type="checkbox" class="mt-0.5 size-4 accent-[var(--ka-accent)]" />
          <span>
            <span class="text-[13px] font-medium text-ink">Show missed savings</span>
            <span class="block text-xs text-ink-2">A what-if over recent transcripts: what keeping every idle session warm would have saved.</span>
          </span>
        </label>
      </fieldset>

      <fieldset>
        <legend class="mb-2 text-[13px] font-semibold text-ink">Appearance</legend>
        <div role="radiogroup" aria-label="Theme" class="inline-flex rounded-lg border border-line p-0.5">
          <label v-for="t in themes" :key="t.id" class="cursor-pointer rounded-md px-3 py-1 text-[13px] has-[:checked]:bg-accent-soft has-[:checked]:font-medium has-[:checked]:text-accent-ink has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent">
            <input v-model="themePref" type="radio" name="ka-theme" :value="t.id" class="sr-only" />{{ t.label }}
          </label>
        </div>
        <p class="mt-1 text-xs text-ink-2">System follows the page embedding this UI, then your system setting.</p>
      </fieldset>

      <UsageReports v-if="store.metrics?.available" />
    </form>
    <template #footer>
      <button type="button" class="btn-ghost" @click="ui.settings = false">Cancel</button>
      <button type="submit" :form="id" class="btn-primary" :disabled="busy || !valid || !dirty">
        <AppIcon v-if="busy" name="lucide:loader-circle" spin /> Save
      </button>
    </template>
  </ModalDialog>
</template>
