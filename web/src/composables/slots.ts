import { reactive } from 'vue'
import { iconNames, type IconName } from '../icons/names'
import type { Harness, SessionView } from '../api/types'

// Slots: places in the UI where the page's add-on (--ui-addon) adds its own
// buttons through the API it gets in setup(ka) (docs/api.md, "Add-on API").

export const SLOTS = ['plan-actions'] as const
export type SlotName = typeof SLOTS[number]

export interface SlotItem {
  id: string
  label: string
  /** a tooltip */
  title?: string
  /** one of the bundled icon names; others are dropped */
  icon?: IconName
  danger?: boolean
}

/** the session an item was clicked for, as the add-on sees it */
export interface SlotSession {
  key: string
  harness: Harness
  id: string
  cwd: string
  project: string
  title: string
}

export type SlotClick = (item: string, session: SlotSession) => unknown

const MAX_ITEMS = 8

/** slots holds the add-on's items, by slot. */
export const slots = reactive<Record<SlotName, SlotItem[]>>({ 'plan-actions': [] })

const handlers: Partial<Record<SlotName, SlotClick>> = {}

const isSlot = (v: unknown): v is SlotName => SLOTS.includes(v as SlotName)
const isIcon = (v: unknown): v is IconName => iconNames.includes(v as IconName)

function text (v: unknown, max: number): string | undefined {
  if (typeof v !== 'string') return undefined
  const t = v.trim()
  return t ? t.slice(0, max) : undefined
}

/** parseItems keeps the well-formed items, with unique ids, at most 8. */
export function parseItems (v: unknown): SlotItem[] {
  if (!Array.isArray(v)) return []
  const out: SlotItem[] = []
  for (const it of v as unknown[]) {
    if (out.length >= MAX_ITEMS) break
    if (!it || typeof it !== 'object') continue
    const o = it as Record<string, unknown>
    const id = text(o.id, 64)
    const label = text(o.label, 40)
    if (!id || !label || out.some(x => x.id === id)) continue
    out.push({ id, label, title: text(o.title, 200), icon: isIcon(o.icon) ? o.icon : undefined, danger: o.danger === true })
  }
  return out
}

/**
 * setSlot replaces a slot's items and its click handler; it reports
 * whether the slot exists. An empty list clears the slot.
 */
export function setSlot (slot: unknown, items: unknown, onClick?: unknown): boolean {
  if (!isSlot(slot)) return false
  slots[slot] = parseItems(items)
  if (typeof onClick === 'function') handlers[slot] = onClick as SlotClick
  else delete handlers[slot]
  return true
}

/** pickSlot hands a click on an item to the add-on's handler. */
export async function pickSlot (slot: SlotName, item: SlotItem, s: SessionView) {
  const session: SlotSession = Object.freeze({ key: s.key, harness: s.harness, id: s.id, cwd: s.cwd, project: s.project, title: s.title })
  try {
    await handlers[slot]?.(item.id, session)
  } catch (e) {
    console.error(`agent-keepalive: add-on click handler for ${slot} failed`, e)
  }
}
