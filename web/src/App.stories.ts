import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { expect, userEvent, within } from 'storybook/test'
import App from './App.vue'
import { store } from './api/store'
import { pinned, ui, viewMode } from './composables/ui'
import { row, TITLES } from './mock/stories'

const meta = {
  title: 'App/Shell',
  component: App,
  parameters: { layout: 'fullscreen', docs: { story: { height: '760px' } } },
} satisfies Meta<typeof App>

export default meta
type Story = StoryObj<typeof meta>

/** Projects at a glance, with the update toast. */
export const AllProjects: Story = {}

export const Project: Story = { parameters: { route: '/p/' + encodeURIComponent('/home/dev/src/agent-keepalive') } }

/** Every session; no update toast (this is the README screenshot). */
export const Sessions: Story = { parameters: { route: '/sessions', mock: { update: 'none' } } }

export const SessionCards: Story = {
  parameters: { route: '/sessions' },
  beforeEach: () => { viewMode.value = 'cards' },
}

export const Pinned: Story = {
  beforeEach: () => { pinned.value = { '/home/dev/src/payments-api': true } },
}

export const Ledger: Story = { parameters: { route: '/ledger' } }

export const Missed: Story = { parameters: { route: '/missed' } }

/** The app inside a 340px side panel. */
export const NarrowPanel: Story = {
  parameters: { route: '/sessions' },
  decorators: [() => ({ template: '<div class="h-[760px] w-[340px] overflow-auto border border-line"><story /></div>' })],
}

/** The narrow layout with its sidebar opened. */
export const NarrowNavOpen: Story = {
  ...NarrowPanel,
  beforeEach: () => { ui.nav = true },
}

export const DrawerOpen: Story = {
  beforeEach: () => { ui.drawer = row(TITLES.indefinite).key },
}

export const SettingsOpen: Story = {
  beforeEach: () => { ui.settings = true },
}

export const AboutOpen: Story = {
  beforeEach: () => { ui.about = true },
}

export const Stopped: Story = {
  beforeEach: () => { store.quit = true },
}

export const Reconnecting: Story = {
  beforeEach: () => { store.link = 'down' },
}

export const NoSessions: Story = {
  beforeEach: () => { store.sessions = [] },
}

/** Start a plan end to end against the mock backend. */
export const StartKeepAlive: Story = {
  parameters: { route: '/sessions' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    const item = canvas.getByRole('button', { name: TITLES.idle }).closest('tr')!
    await userEvent.click(within(item).getByRole('button', { name: /Keep warm/ }))
    await userEvent.click(canvas.getByRole('radio', { name: '2h' }))
    await userEvent.click(canvas.getByRole('button', { name: 'Keep warm 2h' }))
    await expect(await canvas.findByText(`Keeping ${TITLES.idle} warm for 2h`)).toBeInTheDocument()
  },
}

/** The server refused the page's token (401). */
export const TokenRefused: Story = {
  beforeEach: () => { store.authError = true },
}
