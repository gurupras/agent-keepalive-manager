<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
import CopyButton from './CopyButton.vue'
import MarkdownLite from './MarkdownLite.vue'
import { applyUpdate, store } from '../api/store'
import { dismissVersion, showUpdateToast } from '../composables/updates'
import { safeHref } from '../lib/markdown'
import { percent } from '../lib/format'

const id = useId()
const notesOpen = ref(false)
const howOpen = ref(false)

const u = computed(() => store.update)
const version = computed(() => u.value?.latest?.version ?? '')
const page = computed(() => safeHref(u.value?.latest?.page ?? '') ?? safeHref(store.info?.downloadUrl ?? ''))
const phase = computed(() => {
  if (store.updatePhase === 'restarting' || u.value?.state === 'installing') return 'restarting'
  if (u.value?.action === 'restart' && u.value.state === 'ready' && store.updatePhase === 'idle') return 'installed'
  if (store.updatePhase === 'applying' || u.value?.state === 'downloading' || u.value?.state === 'ready') return 'progress'
  if (u.value?.state === 'error') return 'error'
  return 'offer'
})
const busy = computed(() => phase.value === 'progress' || phase.value === 'restarting')
const restartLabel = computed(() => u.value?.action === 'restart' && phase.value === 'error' ? 'Try again' : 'Restart')

function later () {
  if (version.value) dismissVersion(version.value)
  notesOpen.value = howOpen.value = false
}

function openPage () {
  if (page.value) window.open(page.value, '_blank', 'noopener,noreferrer')
}
</script>

<template>
  <section
    v-if="showUpdateToast && u"
    class="pointer-events-auto rounded-ka border border-line bg-surface p-3.5 shadow-pop"
    role="region" :aria-labelledby="id + '-t'"
  >
    <div class="flex items-start gap-2.5">
      <AppIcon name="lucide:download" class="mt-0.5 text-base text-accent" />
      <div class="min-w-0 flex-1">
        <h2 :id="id + '-t'" class="text-[13px] font-semibold text-ink">
          <template v-if="phase === 'restarting'">Restarting agent-keepalive…</template>
          <template v-else-if="phase === 'progress'">Updating to {{ version }}…</template>
          <template v-else-if="phase === 'installed'">agent-keepalive {{ version }} is installed</template>
          <template v-else>agent-keepalive {{ version }} is available</template>
        </h2>
        <p v-if="phase === 'offer'" class="mt-0.5 text-xs text-ink-2">You have {{ u.current }}.</p>
        <p v-else-if="phase === 'installed'" class="mt-0.5 text-xs text-ink-2">{{ u.current }} is still running. Restart to use the new version; keep-alive plans carry over.</p>
        <p v-else-if="phase === 'restarting'" class="mt-0.5 text-xs text-ink-2">The page reconnects by itself when the new version is up.</p>

        <div v-if="phase === 'progress'" class="mt-2">
          <progress class="ka-progress h-1.5 w-full" :value="u.progress || 0" max="1" :aria-label="'Download progress ' + percent(u.progress || 0)"></progress>
          <div class="mt-1 text-[11px] text-ink-2 tabular-nums">{{ u.state === 'ready' ? 'Downloaded and verified' : `Downloading… ${percent(u.progress || 0)}` }}</div>
        </div>
        <div v-if="phase === 'restarting'" class="mt-2 flex items-center gap-1.5 text-xs text-ink-2"><AppIcon name="lucide:loader-circle" spin /> Waiting for the server</div>

        <p v-if="phase === 'error'" class="mt-1.5 text-xs text-danger" role="alert">{{ u.error || 'The update failed.' }}</p>
      </div>
      <button v-if="!busy" type="button" class="icon-btn -mt-1 -mr-1 size-7" aria-label="Dismiss until the next version" @click="later"><AppIcon name="lucide:x" /></button>
    </div>

    <div v-if="notesOpen && u.latest?.notes" :id="id + '-n'" class="mt-2.5 max-h-56 overflow-y-auto rounded-md border border-line bg-base p-2.5">
      <MarkdownLite :source="u.latest.notes" />
    </div>

    <div v-if="howOpen && u.command" class="mt-2.5 rounded-md border border-line bg-base p-2.5">
      <p class="text-xs text-ink-2">This copy was installed by a package manager. Update it with:</p>
      <pre class="mt-1.5 overflow-x-auto rounded bg-surface-2 px-2 py-1.5 mono whitespace-pre-wrap text-ink break-all">{{ u.command }}</pre>
      <div class="mt-1.5 flex justify-end"><CopyButton :text="u.command" label="Copy command" /></div>
    </div>

    <div v-if="!busy" class="mt-3 flex flex-wrap items-center justify-end gap-2">
      <button v-if="u.latest?.notes" type="button" class="btn-ghost mr-auto px-2 text-xs" :aria-expanded="notesOpen" :aria-controls="id + '-n'" @click="notesOpen = !notesOpen">
        <AppIcon :name="notesOpen ? 'lucide:chevron-down' : 'lucide:chevron-right'" /> Release notes
      </button>
      <a v-else-if="page" :href="page" target="_blank" rel="noopener noreferrer" class="btn-ghost mr-auto px-2 text-xs">
        Release notes <AppIcon name="lucide:external-link" />
      </a>
      <button type="button" class="btn-ghost text-xs" @click="later">Later</button>
      <button v-if="u.action === 'restart'" type="button" class="btn-primary text-xs" @click="applyUpdate">
        <AppIcon name="lucide:refresh-cw" /> {{ restartLabel }}
      </button>
      <button v-else-if="u.action === 'apply'" type="button" class="btn-primary text-xs" @click="applyUpdate">
        <AppIcon name="lucide:download" /> {{ phase === 'error' ? 'Try again' : 'Update' }}
      </button>
      <button v-else-if="u.action === 'command'" type="button" class="btn-primary text-xs" :aria-expanded="howOpen" @click="howOpen = !howOpen">
        <AppIcon name="lucide:terminal" /> How to update
      </button>
      <button v-else-if="u.action === 'download' && page" type="button" class="btn-primary text-xs" @click="openPage">
        <AppIcon name="lucide:external-link" /> Download
      </button>
    </div>
  </section>
</template>

<style scoped>
@reference "../style.css";

.ka-progress {
  appearance: none;
  border: 0;
  border-radius: 9999px;
  overflow: hidden;
  background: var(--ka-surface-3);
  accent-color: var(--ka-accent);
}

.ka-progress::-webkit-progress-bar {
  background: var(--ka-surface-3);
}

.ka-progress::-webkit-progress-value {
  background: var(--ka-accent);
  transition: width 0.3s ease;
}

.ka-progress::-moz-progress-bar {
  background: var(--ka-accent);
}
</style>
