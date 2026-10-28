import { createRequire } from 'node:module'
import type { Plugin } from 'vite'
import { getIcons } from '@iconify/utils'
import type { IconifyJSON } from '@iconify/types'
import { iconNames } from '../src/icons/names.ts'

const VIRTUAL_ID = 'virtual:icon-collections'
const RESOLVED_ID = '\0' + VIRTUAL_ID
const require = createRequire(import.meta.url)

/** subsetIcons extracts just the named icons from the installed @iconify-json/<prefix> collections. */
export function subsetIcons (names: readonly string[]): IconifyJSON[] {
  const byPrefix = new Map<string, string[]>()
  for (const full of names) {
    const [prefix, name] = full.split(':')
    if (!prefix || !name) throw new Error(`icon "${full}" must be "prefix:name"`)
    byPrefix.set(prefix, [...(byPrefix.get(prefix) ?? []), name])
  }
  return [...byPrefix].map(([prefix, list]) => {
    const collection = require(`@iconify-json/${prefix}/icons.json`) as IconifyJSON
    const subset = getIcons(collection, list, true)
    const missing = list.filter(n => !subset?.icons[n] && !subset?.aliases?.[n])
    if (!subset || missing.length) {
      throw new Error(`unknown icons in @iconify-json/${prefix}: ${missing.join(', ')}`)
    }
    return subset
  })
}

/**
 * iconCollections serves `virtual:icon-collections`: the icon data the app
 * uses, inlined at build time so no icon API is ever contacted.
 */
export function iconCollections (): Plugin {
  return {
    name: 'agent-keepalive:icon-collections',
    resolveId (id) {
      return id === VIRTUAL_ID ? RESOLVED_ID : undefined
    },
    load (id) {
      if (id !== RESOLVED_ID) return undefined
      return `export default ${JSON.stringify(subsetIcons(iconNames))}`
    },
  }
}
