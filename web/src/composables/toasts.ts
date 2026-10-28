import { reactive } from 'vue'

export type ToastKind = 'info' | 'success' | 'error'

export interface ToastAction {
  label: string
  run: () => void
}

export interface Toast {
  id: number
  kind: ToastKind
  title: string
  body?: string
  action?: ToastAction
}

const MAX = 4
let seq = 0
const timers = new Map<number, ReturnType<typeof setTimeout>>()

/** toasts are the transient notifications, newest last. */
export const toasts = reactive<Toast[]>([])

export function dismiss (id: number) {
  clearTimeout(timers.get(id))
  timers.delete(id)
  const i = toasts.findIndex(t => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}

export interface ToastOptions {
  body?: string
  action?: ToastAction
  /** milliseconds on screen; 0 keeps it until dismissed */
  ms?: number
}

export function toast (kind: ToastKind, title: string, opts: ToastOptions = {}): number {
  const id = ++seq
  toasts.push({ id, kind, title, body: opts.body, action: opts.action })
  while (toasts.length > MAX) dismiss(toasts[0]!.id)
  const ms = opts.ms ?? (kind === 'error' ? 8000 : 4000)
  if (ms > 0) timers.set(id, setTimeout(() => dismiss(id), ms))
  return id
}

export function errorText (err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

export function toastError (title: string, err: unknown) {
  toast('error', title, { body: errorText(err) })
}

/** clearToasts removes every toast (Storybook). */
export function clearToasts () {
  for (const t of [...toasts]) dismiss(t.id)
}
