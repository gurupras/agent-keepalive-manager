import type { Meta, StoryObj } from '@storybook/vue3-vite'
import SessionCard from './SessionCard.vue'
import { row, TITLES } from '../mock/stories'
import { ui } from '../composables/ui'

const meta = {
  title: 'Sessions/SessionCard',
  component: SessionCard,
  decorators: [() => ({ template: '<div class="max-w-lg"><story /></div>' })],
} satisfies Meta<typeof SessionCard>

export default meta
type Story = StoryObj<typeof meta>

const at = (title: string): Story => ({ args: { row: row(title), showProject: true } })

export const Idle: Story = at(TITLES.idle)
export const Kept: Story = at(TITLES.kept)
export const Indefinite: Story = at(TITLES.indefinite)
export const Paused: Story = at(TITLES.paused)
export const Blocked: Story = at(TITLES.blocked)
export const Working: Story = at(TITLES.working)
export const Unreachable: Story = at(TITLES.unreachable)
export const UnknownPrice: Story = at(TITLES.unknownPrice)
export const PanelOpen: Story = {
  ...at(TITLES.idle),
  beforeEach: () => { ui.planFor = row(TITLES.idle).key },
}
