// The add-on API (docs/api.md, "Add-on API"): what an add-on's
// `export function setup (ka)` receives. The app's own code is checked
// against these types, so this file is the contract. Copy it into an
// add-on's project to type-check against it.

/** Where an add-on's items can go. */
export type AddonSlot = 'plan-actions' | 'session-actions'

/** The session an item is checked for or clicked on. */
export interface AddonSession {
  readonly key: string
  readonly harness: 'claude' | 'codex'
  /** the harness's session id */
  readonly id: string
  readonly cwd: string
  readonly project: string
  readonly title: string
}

/** One button in a slot. Strings longer than the limits are cut. */
export interface AddonItem {
  /** up to 64 characters, unique in the slot */
  id: string
  /** up to 40 characters */
  label: string
  /** a tooltip, up to 200 characters */
  title?: string
  /** an icon name the UI bundles (web/src/icons/names.ts); others are dropped */
  icon?: string
  /** shows the item in red */
  danger?: boolean
  /**
   * Whether the item shows on a session; without it, it shows on every one.
   * Called as the UI draws: keep it quick and synchronous. A filter that
   * throws hides its item.
   */
  visible?: (session: AddonSession) => boolean
}

export type AddonClick = (id: string, session: AddonSession) => unknown

export interface AddonApi {
  /** The API version, 1. Later versions only add to it. */
  readonly api: number
  readonly slots: {
    /** the slots this UI has */
    readonly names: readonly AddonSlot[]
    /**
     * Replaces the slot's items (at most 8 are kept) and its click handler;
     * [] clears it. Returns false for a slot this UI does not have. Call it
     * again whenever the items, or what visible would answer, change.
     */
    set (slot: AddonSlot, items: AddonItem[], onClick?: AddonClick): boolean
  }
}

/** What addon.js may export; the app calls it once and awaits it. */
export type AddonSetup = (ka: AddonApi) => unknown
