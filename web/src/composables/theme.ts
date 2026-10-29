import { computed, ref, watchEffect } from 'vue'
import { persisted } from './persisted'

export type ThemePref = 'system' | 'light' | 'dark'

/**
 * The scheme is, strongest first: the user's choice in this UI, the
 * embedding page's <html data-theme="light|dark">, the system preference.
 * The result is written to <html data-ka-scheme>, which style.css keys the
 * palette on.
 */
export const themePref = persisted<ThemePref>('theme', 'system')

const media = window.matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(media.matches)
media.addEventListener('change', e => { systemDark.value = e.matches })

const root = document.documentElement
const hostTheme = ref(root.dataset.theme)
new MutationObserver(() => { hostTheme.value = root.dataset.theme })
  .observe(root, { attributes: true, attributeFilter: ['data-theme'] })

export const dark = computed(() => {
  if (themePref.value !== 'system') return themePref.value === 'dark'
  if (hostTheme.value === 'dark' || hostTheme.value === 'light') return hostTheme.value === 'dark'
  return systemDark.value
})

watchEffect(() => {
  root.dataset.kaScheme = dark.value ? 'dark' : 'light'
})

/** toggleDark flips between light and dark, leaving "system" for an explicit choice. */
export function toggleDark () {
  themePref.value = dark.value ? 'light' : 'dark'
}
