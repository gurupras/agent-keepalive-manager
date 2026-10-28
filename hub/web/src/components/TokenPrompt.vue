<script setup lang="ts">
import { ref } from 'vue'
import { login, Unauthorized } from '../api.ts'

const emit = defineEmits<{ done: [] }>()
const token = ref('')
const busy = ref(false)
const error = ref('')

async function submit () {
  busy.value = true
  error.value = ''
  try {
    await login(token.value.trim())
    token.value = ''
    emit('done')
  } catch (e) {
    error.value = e instanceof Unauthorized ? 'That token is not right.' : (e as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto mt-16 max-w-sm rounded-xl border border-line bg-surface-1 p-6 shadow-card">
    <h2 class="text-lg font-semibold text-text-0">Dashboard token</h2>
    <p class="mt-1 text-sm text-text-2">This hub's dashboard is private. Enter the token its operator gave you.</p>
    <form class="mt-4 space-y-3" @submit.prevent="submit">
      <label class="sr-only" for="hub-token">Token</label>
      <input
        id="hub-token" v-model="token" type="password" autocomplete="current-password" required autofocus
        class="w-full rounded-md border border-line bg-surface-2 px-3 py-2 text-text-0 focus:border-accent focus:outline-none"
      />
      <p v-if="error" class="text-sm text-danger" role="alert">{{ error }}</p>
      <button
        type="submit" :disabled="busy || !token.trim()"
        class="w-full rounded-md bg-accent px-3 py-2 text-sm font-semibold text-on-accent disabled:opacity-50"
      >
        {{ busy ? 'Checking…' : 'Open dashboard' }}
      </button>
    </form>
  </div>
</template>
