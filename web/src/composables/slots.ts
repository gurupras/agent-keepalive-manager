import { reactive } from 'vue'
import { iconNames, type IconName } from '../icons/names'
import type { Harness, SessionView } from '../api/types'

// Slots: places in the UI where the page's add-on (--ui-addon) adds its own
// buttons through the API it gets in setup(ka) (docs/api.md, "Add-on API").

export const SLOTS = ['plan-actions', 'session-actions'] as const
export type SlotName = typeof SLOTS[number]

export interface SlotItem {
  id: string
  label: string
  /** a tooltip */
  title?: string
  /** one of the bundled icon names; others are dropped */
  icon?: IconName
  danger?: boolean
  /** whether the item shows on a session; checked as the UI draws it */
  visible?: (session: SlotSession) => unknown
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
export const slots = reactive<Record<SlotName, SlotItem[]>>({ 'plan-actions': [], 'session-actions': [] })

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
    out.push({
      id,
      label,
      title: text(o.title, 200),
      icon: isIcon(o.icon) ? o.icon : undefined,
      danger: o.danger === true,
      visible: typeof o.visible === 'function' ? o.visible as SlotItem['visible'] : undefined,
    })
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

// One frozen copy per session object; the store replaces a session's object
// when it changes, so a copy never goes stale.
const copies = new WeakMap<SessionView, SlotSession>()

function sessionOf (s: SessionView): SlotSession {
  let c = copies.get(s)
  if (!c) {
    c = Object.freeze({ key: s.key, harness: s.harness, id: s.id, cwd: s.cwd, project: s.project, title: s.title })
    copies.set(s, c)
  }
  return c
}

// A filter that throws hides its item, and is logged once.
const failed = new WeakSet<object>()

/** slotItems is a slot's items that show on a session. */
export function slotItems (slot: SlotName, s: SessionView): SlotItem[] {
  return slots[slot].filter(it => {
    if (!it.visible) return true
    try {
      return !!it.visible(sessionOf(s))
    } catch (e) {
      if (!failed.has(it.visible)) {
        failed.add(it.visible)
        console.error(`agent-keepalive: add-on filter for ${slot}/${it.id} failed`, e)
      }
      return false
    }
  })
}

/** pickSlot hands a click on an item to the add-on's handler. */
export async function pickSlot (slot: SlotName, item: SlotItem, s: SessionView) {
  try {
    await handlers[slot]?.(item.id, sessionOf(s))
  } catch (e) {
    console.error(`agent-keepalive: add-on click handler for ${slot} failed`, e)
  }
}
