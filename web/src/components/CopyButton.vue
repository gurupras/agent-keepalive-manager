<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { copyText } from '../composables/clipboard'
import { toast } from '../composables/toasts'

const props = withDefaults(defineProps<{ text: string, label?: string }>(), { label: 'Copy' })
const done = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy () {
  if (await copyText(props.text)) {
    done.value = true
    clearTimeout(timer)
    timer = setTimeout(() => { done.value = false }, 1500)
  } else {
    toast('error', 'Could not copy', { body: 'Select the text and copy it by hand.' })
  }
}
</script>

<template>
  <button type="button" class="btn-outline px-2 py-1 text-xs" :aria-label="done ? 'Copied' : label" @click="copy">
    <AppIcon :name="done ? 'lucide:check' : 'lucide:copy'" :class="done ? 'text-ok' : ''" />
    <span>{{ done ? 'Copied' : label }}</span>
  </button>
</template>
