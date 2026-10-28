/** copyText copies s to the clipboard; false if the browser refused. */
export async function copyText (s: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(s)
    return true
  } catch {
    // Not a secure context or no permission: fall back to a hidden textarea.
    try {
      const ta = document.createElement('textarea')
      ta.value = s
      ta.setAttribute('readonly', '')
      ta.className = 'fixed -left-[9999px] opacity-0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      ta.remove()
      return ok
    } catch {
      return false
    }
  }
}
