import { setSlot, SLOTS } from './slots'

// The add-on (docs/api.md, "Add-on API"): when the page carries
// <meta name="ka-addon">, the app imports ./addon/addon.js and, if it
// exports setup, calls it with the API. The address is the one the
// add-on's own script tag uses, so the module runs once.

/** API is the add-on API's version; it grows only by additions. */
export const API = 1

const ka = Object.freeze({
  api: API,
  slots: Object.freeze({ names: SLOTS, set: setSlot }),
})

/** loadAddon runs the add-on's setup; a failing add-on is logged, never fatal. */
export async function loadAddon () {
  if (!document.querySelector('meta[name="ka-addon"]')) return
  try {
    const mod = await import(/* @vite-ignore */ new URL('./addon/addon.js', document.baseURI).href) as { setup?: unknown }
    if (typeof mod.setup === 'function') await mod.setup(ka)
  } catch (e) {
    console.error('agent-keepalive: add-on setup failed', e)
  }
}
