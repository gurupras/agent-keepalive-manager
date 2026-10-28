import type { Meta, StoryObj } from '@storybook/vue3-vite'
import PingHistory from './PingHistory.vue'
import type { PingRecord, PingState } from '../api/types'
import { FROZEN_NOW } from '../mock/server'

const states: PingState[] = ['sending', 'delivered', 'seen', 'verified', 'failed', 'withdrawn', 'lost']
const pings: PingRecord[] = [...states, 'verified' as const].map((state, i) => ({
  id: 'p' + i,
  planId: 1,
  key: 'claude:x',
  sentAt: new Date(FROZEN_NOW - i * 55 * 60_000).toISOString(),
  state,
  calls: state === 'verified' ? 1 : 0,
  usage: { input: 12, output: 4, cacheRead: i === 7 ? 0 : 412_000, cacheWrite5m: 0, cacheWrite1h: i === 7 ? 412_000 : 40 },
  cost: state === 'verified' ? (i === 7 ? 4.12 : 0.21) : 0,
  cold: i === 7,
  error: state === 'failed' ? 'The messaging socket refused the connection.' : undefined,
}))

const meta = {
  title: 'Sessions/PingHistory',
  component: PingHistory,
  decorators: [() => ({ template: '<div class="max-w-md"><story /></div>' })],
  args: { pings },
} satisfies Meta<typeof PingHistory>

export default meta
type Story = StoryObj<typeof meta>

/** Every ping state, and a cold verified ping. */
export const AllStates: Story = {}
export const Empty: Story = { args: { pings: [] } }
export const Loading: Story = { args: { pings: undefined } }
