import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UpdateToast from './UpdateToast.vue'
import { store } from '../api/store'

const meta = {
  title: 'Chrome/UpdateToast',
  component: UpdateToast,
  decorators: [() => ({ template: '<div class="max-w-[26rem]"><story /></div>' })],
} satisfies Meta<typeof UpdateToast>

export default meta
type Story = StoryObj<typeof meta>

export const Apply: Story = {}
export const Command: Story = { parameters: { mock: { update: 'command' } } }
export const Download: Story = { parameters: { mock: { update: 'download' } } }
export const Downloading: Story = {
  beforeEach: () => { store.update = { ...store.update!, state: 'downloading', progress: 0.42 } },
}
export const Restarting: Story = {
  beforeEach: () => { store.updatePhase = 'restarting' },
}
export const Failed: Story = {
  beforeEach: () => { store.update = { ...store.update!, state: 'error', error: 'The download did not match the signed checksum.' } },
}
export const Installed: Story = {
  beforeEach: () => { store.update = { ...store.update!, state: 'ready', action: 'restart', installed: store.update!.latest?.version } },
}
