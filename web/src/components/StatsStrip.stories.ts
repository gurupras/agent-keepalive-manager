import type { Meta, StoryObj } from '@storybook/vue3-vite'
import StatsStrip from './StatsStrip.vue'
import { rows } from '../composables/ui'
import { store } from '../api/store'

const meta = {
  title: 'Layout/StatsStrip',
  component: StatsStrip,
} satisfies Meta<typeof StatsStrip>

export default meta
type Story = StoryObj<typeof meta>

export const AllSessions: Story = { args: { rows: [] }, render: () => ({ components: { StatsStrip }, setup: () => ({ rows }), template: '<StatsStrip :rows="rows" />' }) }

/** Scoped to one project, with missed savings on. */
export const Project: Story = {
  args: { rows: [] },
  beforeEach: () => { if (store.settings) store.settings.showMissed = true },
  render: () => ({
    components: { StatsStrip },
    setup: () => ({ rows: rows.value.filter(r => r.s.project.endsWith('/agent-keepalive')), project: rows.value.find(r => r.s.project.endsWith('/agent-keepalive'))!.s.project }),
    template: '<StatsStrip :rows="rows" :project="project" />',
  }),
}
