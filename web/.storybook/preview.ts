import { setup, type Preview } from '@storybook/vue3-vite'
import { router } from '../src/router'
import { registerIcons } from '../src/icons'
import { freezeNow } from '../src/composables/now'
import { themePref } from '../src/composables/theme'
import { FROZEN_NOW, type MockOptions } from '../src/mock/server'
import { seed } from '../src/mock/stories'
import '../src/style.css'

// Same bootstrapping as src/main.ts, against a frozen mock backend.
registerIcons()
freezeNow(FROZEN_NOW)
setup(app => { app.use(router) })

declare module 'storybook/internal/types' {
  interface Parameters {
    /** route the shared router moves to before the story renders */
    route?: string
    /** mock backend scenario */
    mock?: MockOptions
  }
}

const preview: Preview = {
  parameters: {
    layout: 'padded',
    controls: { matchers: { color: /(background|color)$/i, date: /Date$/i } },
    // Stories share app-wide state, so each docs entry gets its own iframe.
    docs: { story: { inline: false, height: '360px' } },
    a11y: { test: 'todo' },
  },
  globalTypes: {
    theme: {
      description: 'Colour scheme',
      toolbar: {
        title: 'Theme',
        icon: 'mirror',
        items: [
          { value: 'light', icon: 'sun', title: 'Light' },
          { value: 'dark', icon: 'moon', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
  },
  initialGlobals: { theme: 'light' },
  async beforeEach ({ parameters }) {
    seed(parameters.mock)
    await router.replace(parameters.route ?? '/')
  },
  decorators: [
    (story, { globals }) => {
      themePref.value = globals.theme === 'dark' ? 'dark' : 'light'
      return { components: { story }, template: '<div class="bg-base text-ink p-1"><story /></div>' }
    },
  ],
  tags: ['autodocs'],
}

export default preview
