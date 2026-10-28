import type { Meta, StoryObj } from '@storybook/vue3-vite'
import MarkdownLite from './MarkdownLite.vue'

const meta = {
  title: 'Chrome/MarkdownLite',
  component: MarkdownLite,
  args: {
    source: '## Release 1.5.0\n\n- **Bold** and *em* and `code`\n- A [link](https://example.com) and https://example.org/x\n\n1. ordered\n2. list\n\n```\nagent-keepalive update\n```\n\n<script>not rendered as HTML</script> [bad](javascript:alert(1))',
  },
  decorators: [() => ({ template: '<div class="max-w-md"><story /></div>' })],
} satisfies Meta<typeof MarkdownLite>

export default meta
type Story = StoryObj<typeof meta>

export const Notes: Story = {}
