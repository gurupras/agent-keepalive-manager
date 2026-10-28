import type { Meta, StoryObj } from '@storybook/vue3-vite'
import PlanPanel from './PlanPanel.vue'
import { row, TITLES } from '../mock/stories'

const meta = {
  title: 'Sessions/PlanPanel',
  component: PlanPanel,
  decorators: [() => ({ template: '<div class="card max-w-lg p-4"><story /></div>' })],
} satisfies Meta<typeof PlanPanel>

export default meta
type Story = StoryObj<typeof meta>

const at = (title: string): Story => ({ args: { row: row(title) }, render: () => ({ components: { PlanPanel }, setup: () => ({ r: row(title) }), template: '<PlanPanel :row="r" />' }) })

/** Opus: capped by break-even below the 36h setting. */
export const BreakEvenCap: Story = at(TITLES.idle)
/** Codex prices: a short break-even. */
export const ShortBreakEven: Story = at(TITLES.working)
export const UnknownPrice: Story = at(TITLES.unknownPrice)
