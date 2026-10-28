import type { Meta, StoryObj } from '@storybook/vue3-vite'
import ToastHost from './ToastHost.vue'
import { toast } from '../composables/toasts'

const meta = {
  title: 'Chrome/ToastHost',
  component: ToastHost,
  parameters: { layout: 'fullscreen', docs: { story: { height: '420px' } }, mock: { update: 'none' } },
  beforeEach: () => {
    toast('success', 'Keeping Ledger idempotency tests warm for 4h', { ms: 0 })
    toast('error', 'Ping failed: One-off backfill script', { body: 'The messaging socket refused the connection.', ms: 0 })
    toast('success', 'Updated to 1.5.0', { body: 'Reload to use the new version of this page.', action: { label: 'Reload', run: () => {} }, ms: 0 })
  },
} satisfies Meta<typeof ToastHost>

export default meta
type Story = StoryObj<typeof meta>

export const Stack: Story = {}
