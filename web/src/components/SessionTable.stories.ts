import type { Meta, StoryObj } from '@storybook/vue3-vite'
import SessionTable from './SessionTable.vue'
import { row, TITLES } from '../mock/stories'
import { ui } from '../composables/ui'

const meta = {
  title: 'Sessions/SessionTable',
  component: SessionTable,
  decorators: [() => ({ template: '<div class="card max-w-5xl"><story /></div>' })],
} satisfies Meta<typeof SessionTable>

export default meta
type Story = StoryObj<typeof meta>

const all = () => Object.values(TITLES).map(row)

/** Every state, one per row. */
export const States: Story = { args: { rows: [] }, render: () => ({ components: { SessionTable }, setup: () => ({ rows: all() }), template: '<SessionTable :rows="rows" show-project />' }) }

export const PanelOpen: Story = {
  args: { rows: [] },
  beforeEach: () => { ui.planFor = row(TITLES.idle).key },
  render: () => ({ components: { SessionTable }, setup: () => ({ rows: [row(TITLES.kept), row(TITLES.idle)] }), template: '<SessionTable :rows="rows" />' }),
}

/** Narrow container: rows stack. */
export const Narrow: Story = {
  ...States,
  decorators: [() => ({ template: '<div class="w-[340px]"><story /></div>' })],
}
