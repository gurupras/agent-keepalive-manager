<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import CopyButton from './CopyButton.vue'
import ModalDialog from './ModalDialog.vue'
import { store } from '../api/store'
import { ui } from '../composables/ui'
import { safeHref } from '../lib/markdown'
import { dateTime, parseTime } from '../lib/format'

const kinds: Record<string, string> = {
  deb: 'Debian package (apt)',
  rpm: 'RPM package (dnf)',
  arch: 'Arch package (pacman)',
  brew: 'Homebrew',
  appimage: 'AppImage',
  'windows-setup': 'Windows installer',
  'windows-portable': 'Windows portable',
  'macos-pkg': 'macOS installer package',
  archive: 'Archive',
  dev: 'Development build',
}

const i = computed(() => store.info)
const home = computed(() => safeHref(i.value?.homepage ?? ''))
const lastCheck = computed(() => {
  const t = parseTime(store.update?.lastCheck)
  return t ? dateTime(t) : ''
})
</script>

<template>
  <ModalDialog :open="ui.about" title="About agent-keepalive" width="w-[28rem]" @close="ui.about = false">
    <template #icon><AppIcon name="lucide:heart-pulse" class="text-accent" /></template>
    <p class="text-[13px] text-ink-1">Keeps the prompt caches of your Claude Code and Codex sessions warm while you are away, and keeps an honest ledger of what that cost and saved.</p>
    <dl v-if="i" class="mt-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-[13px]">
      <dt class="text-ink-2">Version</dt>
      <dd class="text-ink tabular-nums">{{ i.version }}<span v-if="lastCheck" class="text-ink-2"> · checked {{ lastCheck }}</span></dd>
      <dt class="text-ink-2">Platform</dt>
      <dd class="text-ink">{{ i.os }}/{{ i.arch }}</dd>
      <dt class="text-ink-2">Profile</dt>
      <dd class="text-ink">{{ i.profile || 'none' }}</dd>
      <dt class="text-ink-2">Installed as</dt>
      <dd class="text-ink">{{ kinds[i.installKind] ?? i.installKind }}<span v-if="i.supervised" class="text-ink-2"> · supervised</span></dd>
      <dt class="text-ink-2">Data directory</dt>
      <dd class="flex min-w-0 items-start gap-2">
        <span class="mono min-w-0 break-all text-ink">{{ i.dataDir }}</span>
        <CopyButton :text="i.dataDir" label="Copy" class="shrink-0" />
      </dd>
    </dl>
    <p v-else class="mt-4 text-[13px] text-ink-2">Not connected.</p>
    <p v-if="home" class="mt-4">
      <a :href="home" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 text-[13px] text-accent-ink underline underline-offset-2">
        Project page <AppIcon name="lucide:external-link" />
      </a>
    </p>
  </ModalDialog>
</template>
