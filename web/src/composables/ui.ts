import { computed, reactive } from 'vue'
import { now } from './now'
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
})

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
  else return false
  return true
}
