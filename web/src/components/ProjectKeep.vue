<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
import { presets } from '../lib/derive'
import { hoursLabel } from '../lib/format'
import { removeProjectRule, setProjectRule, store } from '../api/store'

// Keeps every session of a project warm with one setting, including the
// sessions started later (a project rule on the server).
const props = defineProps<{ project: string }>()

type Choice = number | 'indefinite'

const id = useId()
const rule = computed(() => store.projectRules?.find(r => r.project === props.project))
const options = computed(() => presets(store.settings?.maxHours ?? 36))
const editing = ref(false)
const busy = ref(false)
const choice = ref<Choice>('indefinite')

const current = computed(() => {
  const r = rule.value
  if (!r) return ''
  return r.indefinite ? 'until you stop it' : `for ${hoursLabel(r.hours ?? 0)} at a time`
})

function edit () {
  const r = rule.value
  choice.value = r && !r.indefinite && r.hours ? r.hours : 'indefinite'
  editing.value = true
}

async function save () {
  if (busy.value) return
  busy.value = true
  const ok = await setProjectRule(props.project, choice.value === 'indefinite' ? { indefinite: true } : { hours: choice.value })
  busy.value = false
  if (ok) editing.value = false
}

async function stop () {
  if (busy.value) return
  busy.value = true
  await removeProjectRule(props.project)
  busy.value = false
  editing.value = false
}

const chipCls = (on: boolean, tone = 'accent') => on
  ? tone === 'warm' ? 'border-warm bg-warm-soft text-warm' : 'border-accent bg-accent-soft text-accent-ink'
  : 'border-line text-ink-1 hover:border-ink-3'
</script>

<template>
  <div v-if="store.projectRules" class="card mt-4 px-4 py-3">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
      <AppIcon :name="rule ? 'lucide:flame' : 'lucide:folder'" :class="rule ? 'text-warm' : 'text-ink-2'" />
      <p class="min-w-0 flex-1 text-[13px] text-ink-1">
        <template v-if="rule">
          Every session in this project is kept warm <strong class="text-ink">{{ current }}</strong>, including new ones.
        </template>
        <template v-else>
          Keep every session in this project warm with one setting, including sessions started later.
        </template>
      </p>
      <template v-if="!editing">
        <button v-if="rule" type="button" class="btn-ghost" :disabled="busy" @click="edit">Change</button>
        <button v-if="rule" type="button" class="btn-ghost" :disabled="busy" @click="stop">
          <AppIcon name="lucide:ban" />Stop
        </button>
        <button v-else type="button" class="btn-soft" @click="edit">
          <AppIcon name="lucide:flame" />Keep project warm
        </button>
      </template>
    </div>

    <form v-if="editing" class="mt-3" @submit.prevent="save" @keydown.esc.stop="editing = false">
      <div :id="id + '-l'" class="label">Keep each session warm for</div>
      <div role="radiogroup" :aria-labelledby="id + '-l'" class="mt-1.5 flex flex-wrap gap-1.5">
        <button
          v-for="h in options" :key="h" type="button" role="radio" :aria-checked="choice === h"
          class="cursor-pointer rounded-lg border px-2.5 py-1 text-[13px] font-medium tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-accent"
          :class="chipCls(choice === h)"
          @click="choice = h"
        >{{ hoursLabel(h) }}</button>
        <button
          type="button" role="radio" :aria-checked="choice === 'indefinite'"
          class="inline-flex cursor-pointer items-center gap-1 rounded-lg border px-2.5 py-1 text-[13px] font-medium outline-none focus-visible:ring-2 focus-visible:ring-warm"
          :class="chipCls(choice === 'indefinite', 'warm')"
          @click="choice = 'indefinite'"
        ><AppIcon name="lucide:infinity" />Until I stop it</button>
      </div>
      <p class="mt-2 flex gap-1.5 text-xs text-ink-2">
        <AppIcon name="lucide:info" class="mt-px" />
        <span>
          Each session gets its own plan, capped at its model's break-even. A session you stop yourself stays
          stopped; one whose plan runs out starts again when you next prompt it. Sessions you already keep warm
          yourself keep their own plan.
        </span>
      </p>
      <div class="mt-3 flex flex-wrap justify-end gap-2">
        <button type="button" class="btn-ghost" @click="editing = false">Cancel</button>
        <button type="submit" class="btn-primary" :class="choice === 'indefinite' ? 'bg-warm' : ''" :disabled="busy">
          <AppIcon :name="busy ? 'lucide:loader-circle' : choice === 'indefinite' ? 'lucide:infinity' : 'lucide:flame'" :spin="busy" />
          {{ choice === 'indefinite' ? 'Keep project warm until stopped' : `Keep project warm, ${hoursLabel(choice)} at a time` }}
        </button>
      </div>
    </form>
  </div>
</template>
