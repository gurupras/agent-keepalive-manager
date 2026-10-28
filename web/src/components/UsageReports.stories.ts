import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UsageReports from './UsageReports.vue'

const meta = {
  title: 'Settings/UsageReports',
  component: UsageReports,
  decorators: [() => ({ template: '<div class="card max-w-xl p-4"><story /></div>' })],
} satisfies Meta<typeof UsageReports>

export default meta
type Story = StoryObj<typeof meta>

export const On: Story = {}
export const Off: Story = { parameters: { mock: { metrics: 'off' } } }
export const LockedByAdmin: Story = { parameters: { mock: { metrics: 'admin' } } }
export const LockedByProfile: Story = { parameters: { mock: { metrics: 'profile' } } }
