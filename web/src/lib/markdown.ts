// A small Markdown subset for release notes, parsed into plain data that a
// component renders with ordinary template nodes. Nothing is ever inserted
// as HTML, so notes cannot inject markup, and only http(s) links are kept.

export type Inline =
  | { t: 'text', text: string }
  | { t: 'code', text: string }
  | { t: 'strong', text: string }
  | { t: 'em', text: string }
  | { t: 'link', text: string, href: string }

export type Block =
  | { t: 'h', level: 1 | 2 | 3, inl: Inline[] }
  | { t: 'p', inl: Inline[] }
  | { t: 'ul' | 'ol', items: Inline[][] }
  | { t: 'pre', text: string }

const INLINE = /`([^`]+)`|\*\*(.+?)\*\*|__(.+?)__|\*([^*\s][^*]*?)\*|_([^_\s][^_]*?)_|\[([^\]]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?])/g

export function safeHref (href: string): string | undefined {
  try {
    const u = new URL(href)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.toString() : undefined
  } catch {
    return undefined
  }
}

export function parseInline (s: string): Inline[] {
  const out: Inline[] = []
  let last = 0
  const text = (t: string) => {
    if (!t) return
    const prev = out.at(-1)
    if (prev?.t === 'text') prev.text += t
    else out.push({ t: 'text', text: t })
  }
  for (const m of s.matchAll(INLINE)) {
    text(s.slice(last, m.index))
    last = m.index + m[0].length
    if (m[1] !== undefined) out.push({ t: 'code', text: m[1] })
    else if (m[2] !== undefined || m[3] !== undefined) out.push({ t: 'strong', text: m[2] ?? m[3]! })
    else if (m[4] !== undefined || m[5] !== undefined) out.push({ t: 'em', text: m[4] ?? m[5]! })
    else if (m[6] !== undefined) {
      const href = safeHref(m[7]!)
      if (href) out.push({ t: 'link', text: m[6], href })
      else text(m[6])
    } else if (m[8] !== undefined) {
      const href = safeHref(m[8])
      if (href) out.push({ t: 'link', text: m[8], href })
      else text(m[8])
    }
  }
  text(s.slice(last))
  return out
}

export function parseMarkdown (src: string): Block[] {
  const blocks: Block[] = []
  const lines = src.replace(/\r\n?/g, '\n').split('\n')
  let para: string[] = []
  let list: { t: 'ul' | 'ol', items: string[] } | undefined

  const flushPara = () => {
    if (para.length) blocks.push({ t: 'p', inl: parseInline(para.join(' ')) })
    para = []
  }
  const flushList = () => {
    if (list) blocks.push({ t: list.t, items: list.items.map(parseInline) })
    list = undefined
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!
    if (/^\s*```/.test(line)) {
      flushPara()
      flushList()
      const code: string[] = []
      while (++i < lines.length && !/^\s*```/.test(lines[i]!)) code.push(lines[i]!)
      blocks.push({ t: 'pre', text: code.join('\n') })
      continue
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line)
    if (h) {
      flushPara()
      flushList()
      blocks.push({ t: 'h', level: Math.min(h[1]!.length, 3) as 1 | 2 | 3, inl: parseInline(h[2]!.trim()) })
      continue
    }
    const li = /^\s*(?:([-*+])|(\d+)[.)])\s+(.*)$/.exec(line)
    if (li) {
      flushPara()
      const t = li[1] ? 'ul' : 'ol'
      if (list?.t !== t) {
        flushList()
        list = { t, items: [] }
      }
      list.items.push(li[3]!.trim())
      continue
    }
    if (!line.trim()) {
      flushPara()
      flushList()
      continue
    }
    // A continuation line of a list item, or paragraph text.
    if (list && /^\s+/.test(line)) list.items[list.items.length - 1] += ' ' + line.trim()
    else {
      flushList()
      para.push(line.trim())
    }
  }
  flushPara()
  flushList()
  return blocks
}
