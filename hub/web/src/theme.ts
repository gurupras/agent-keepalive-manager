// Light, dark or the system's choice, remembered in localStorage and
// written to <html data-ka-scheme>.

import { ref, watchEffect } from 'vue'

export type ThemeChoice = 'system' | 'light' | 'dark'
const KEY = 'agent-keepalive-hub.theme'

function stored (): ThemeChoice {
  try {
    const v = window.localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    // storage blocked
  }
  return 'system'
}

export const theme = ref<ThemeChoice>(stored())
const media = window.matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(media.matches)
media.addEventListener('change', e => { systemDark.value = e.matches })

watchEffect(() => {
  const dark = theme.value === 'dark' || (theme.value === 'system' && systemDark.value)
  document.documentElement.dataset.kaScheme = dark ? 'dark' : 'light'
  try {
    if (theme.value === 'system') window.localStorage.removeItem(KEY)
    else window.localStorage.setItem(KEY, theme.value)
  } catch {
    // storage blocked
  }
})

export function cycleTheme () {
  theme.value = theme.value === 'system' ? 'light' : theme.value === 'light' ? 'dark' : 'system'
}
