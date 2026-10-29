import { computed, reactive, ref } from 'vue'
import { now } from './now'
import { persisted } from './persisted'
import { store } from '../api/store'
import { groupProjects, toRow } from '../lib/derive'

/** ui holds transient view state shared across components. */
export const ui = reactive({
  /** key of the session shown in the details drawer */
  drawer: null as string | null,
  /** key of the session whose Keep warm panel is open */
  planFor: null as string | null,
  settings: false,
  about: false,
  /** the sidebar is open over the page (narrow windows only) */
  nav: false,
  /** the sidebar's project filter */
  search: '',
})

/** searchInput is the sidebar's project filter, focused by the / key. */
export const searchInput = ref<HTMLInputElement | null>(null)

/** pinned projects, by id, listed first in the sidebar */
export const pinned = persisted<Record<string, boolean>>('pinned', {})

/** how session lists are laid out */
export const viewMode = persisted<'cards' | 'table'>('session-view', 'table')

export function togglePin (id: string) {
  if (pinned.value[id]) delete pinned.value[id]
  else pinned.value[id] = true
}

/** rows are all live sessions, with live countdowns. */
export const rows = computed(() => store.sessions.map(s => toRow(s, now.value)))

export const projects = computed(() => groupProjects(rows.value))

export const drawerRow = computed(() => rows.value.find(r => r.key === ui.drawer))

/** closeTopmost closes the topmost overlay; false if none was open. */
export function closeTopmost (): boolean {
  if (ui.settings) ui.settings = false
  else if (ui.about) ui.about = false
  else if (ui.drawer) ui.drawer = null
  else if (ui.planFor) ui.planFor = null
  else if (ui.nav) ui.nav = false
  else return false
  return true
}
