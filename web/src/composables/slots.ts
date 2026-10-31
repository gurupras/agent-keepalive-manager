import { reactive } from 'vue'
import { iconNames, type IconName } from '../icons/names'
import type { SessionView } from '../api/types'

// Slots: places in the UI where the page that frames it adds its own
// buttons (docs/api.md, "Slots"). The parent posts the items for a slot; a
// click on one is posted back to it with the session. Only the parent
// window is heard: frame-ancestors decides which pages may be it.

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

const MAX_ITEMS = 8

/** slots holds the parent's items, by slot. */
export const slots = reactive<Record<SlotName, SlotItem[]>>({ 'plan-actions': [] })

/** the origin of the parent that set the items; clicks go only to it */
let owner = ''

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

function onMessage (e: MessageEvent) {
  // A sandboxed parent's origin is "null", which postMessage cannot target.
  if (e.source !== window.parent || e.origin === 'null') return
  const d = e.data as { type?: unknown, slot?: unknown, items?: unknown } | null
  if (!d || typeof d !== 'object' || d.type !== 'agent-keepalive:slot' || !isSlot(d.slot)) return
  owner = e.origin
  slots[d.slot] = parseItems(d.items)
}

/** watchSlots listens to the parent, when framed, and tells it the UI is ready. */
export function watchSlots () {
  if (window.parent === window) return
  window.addEventListener('message', onMessage)
  window.parent.postMessage({ type: 'agent-keepalive:ready', slots: SLOTS }, '*')
}

/** pickSlot tells the parent that an item was clicked for a session. */
export function pickSlot (slot: SlotName, item: SlotItem, s: SessionView) {
  if (!owner) return
  window.parent.postMessage({
    type: 'agent-keepalive:slot-click',
    slot,
    item: item.id,
    session: { key: s.key, harness: s.harness, id: s.id, cwd: s.cwd, project: s.project, title: s.title },
  }, owner)
}
