import { watch } from 'vue'
import { store } from '../api/store'
import { readStored, writeStored } from './persisted'
import { toast } from './toasts'
import { ui } from './ui'

const KEY = 'usage-reports-notice'

/**
 * watchUsageNotice says once, the first time the app opens with usage
 * reports on, that they are on and where to turn them off. The first report
 * of an install goes out a few minutes after start-up (docs/metrics.md).
 */
export function watchUsageNotice () {
  const stop = watch(() => store.loaded && store.metrics, m => {
    if (!m || !m.available || !m.enabled || readStored<boolean>(KEY)) return
    writeStored(KEY, true)
    toast('info', 'Anonymous usage reports are on', {
      body: 'agent-keepalive sends its hub a daily summary of what keeping caches warm saved, and a first one a few minutes after it is installed or updated. No prompts, paths or names are sent.',
      action: { label: 'Usage report settings', run: () => { ui.settings = true } },
      ms: 0,
    })
    queueMicrotask(() => stop())
  }, { immediate: true })
}
