import type { Meta, StoryObj } from '@storybook/vue3-vite'
import SessionRow from './SessionRow.vue'
import { row, TITLES } from '../mock/stories'
import { ui } from '../composables/ui'

const meta = {
  title: 'Sessions/SessionRow',
  component: SessionRow,
  decorators: [() => ({ template: '<ul class="card @container max-w-4xl"><story /></ul>' })],
} satisfies Meta<typeof SessionRow>

export default meta
type Story = StoryObj<typeof meta>

const at = (title: string): Story => ({ args: { row: row(title) }, render: () => ({ components: { SessionRow }, setup: () => ({ r: row(title) }), template: '<SessionRow :row="r" />' }) })

export const Idle: Story = at(TITLES.idle)
export const Kept: Story = at(TITLES.kept)
export const Indefinite: Story = at(TITLES.indefinite)
export const Paused: Story = at(TITLES.paused)
export const Blocked: Story = at(TITLES.blocked)
export const Working: Story = at(TITLES.working)
export const Stale: Story = at(TITLES.stale)
export const Cold: Story = at(TITLES.cold)
export const Unreachable: Story = at(TITLES.unreachable)
export const UnknownPrice: Story = at(TITLES.unknownPrice)
export const PanelOpen: Story = {
  ...at(TITLES.idle),
  beforeEach: () => { ui.planFor = row(TITLES.idle).key },
}
/** Narrow container: the row stacks. */
export const Narrow: Story = {
  ...at(TITLES.kept),
  decorators: [() => ({ template: '<div class="w-[320px]"><story /></div>' })],
}
