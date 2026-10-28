import type { StorybookConfig } from '@storybook/vue3-vite'

// The project's vite.config.ts (Vue, Tailwind, offline icons) is loaded by
// the Vite builder. Stories use the in-browser mock backend (src/mock).
const config: StorybookConfig = {
  stories: ['../src/**/*.stories.ts'],
  addons: ['@storybook/addon-docs', '@storybook/addon-a11y'],
  framework: { name: '@storybook/vue3-vite', options: {} },
  core: { disableTelemetry: true },
  features: { backgrounds: false },
}

export default config
