import type { Meta, StoryObj } from '@storybook/vue3-vite'
import CacheCountdown from './CacheCountdown.vue'
import CacheBar from './CacheBar.vue'
import { row, TITLES } from '../mock/stories'

const meta = {
  title: 'Sessions/CacheCountdown',
  component: CacheCountdown,
} satisfies Meta<typeof CacheCountdown>

export default meta
type Story = StoryObj<typeof meta>

/** Every cache state, with its bar. */
export const States: Story = {
  args: { row: row(TITLES.idle) },
  render: () => ({
    components: { CacheCountdown, CacheBar },
    setup: () => ({ rows: [TITLES.kept, TITLES.idle, TITLES.working, TITLES.stale, TITLES.cold].map(row) }),
    template: '<div class="grid w-48 gap-3"><div v-for="r in rows" :key="r.key"><CacheCountdown :row="r" /><CacheBar :row="r" class="mt-1" /></div></div>',
  }),
}
