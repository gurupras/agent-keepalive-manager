import { nextTick, onBeforeUnmount, ref, type ShallowRef } from 'vue'

/**
 * useMenu runs a dropdown menu inside `root`: its trigger is the element
 * with aria-haspopup, its items the enabled [role="menuitem"] elements. A
 * click outside, Escape or Tab closes it; arrows, Home and End move focus.
 */
export function useMenu (root: Readonly<ShallowRef<HTMLElement | null>>, onClose?: () => void) {
  const open = ref(false)

  const items = () => [...(root.value?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? [])]

  function onDoc (e: MouseEvent) {
    if (!root.value?.contains(e.target as Node)) close()
  }

  async function toggle () {
    if (open.value) return close()
    open.value = true
    document.addEventListener('mousedown', onDoc)
    await nextTick()
    items()[0]?.focus()
  }

  function close (refocus = false) {
    open.value = false
    document.removeEventListener('mousedown', onDoc)
    onClose?.()
    if (refocus) root.value?.querySelector<HTMLElement>('[aria-haspopup]')?.focus()
  }

  function onKey (e: KeyboardEvent) {
    if (!open.value) return
    const list = items()
    const i = list.indexOf(document.activeElement as HTMLElement)
    if (e.key === 'ArrowDown') list[(i + 1) % list.length]?.focus()
    else if (e.key === 'ArrowUp') list[(i - 1 + list.length) % list.length]?.focus()
    else if (e.key === 'Home') list[0]?.focus()
    else if (e.key === 'End') list.at(-1)?.focus()
    else if (e.key === 'Escape' || e.key === 'Tab') {
      if (e.key === 'Escape') e.stopPropagation()
      close(e.key === 'Escape')
      return
    } else return
    e.preventDefault()
  }

  onBeforeUnmount(() => close())

  return { open, toggle, close, onKey }
}
