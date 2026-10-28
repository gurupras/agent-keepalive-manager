import type { Meta, StoryObj } from '@storybook/vue3-vite'
import PlanStatus from './PlanStatus.vue'
import { row, TITLES } from '../mock/stories'

const meta = {
  title: 'Sessions/PlanStatus',
  component: PlanStatus,
  decorators: [() => ({ template: '<div class="max-w-md"><story /></div>' })],
} satisfies Meta<typeof PlanStatus>

export default meta
type Story = StoryObj<typeof meta>

const at = (title: string, compact = false): Story => ({
  args: { row: row(title), compact },
  render: () => ({ components: { PlanStatus }, setup: () => ({ r: row(title), compact }), template: '<PlanStatus :row="r" :compact="compact" />' }),
})

export const Active: Story = at(TITLES.kept)
export const Indefinite: Story = at(TITLES.indefinite)
export const Paused: Story = at(TITLES.paused)
export const Blocked: Story = at(TITLES.blocked)
export const CompactActive: Story = at(TITLES.kept, true)
export const CompactPaused: Story = at(TITLES.paused, true)
