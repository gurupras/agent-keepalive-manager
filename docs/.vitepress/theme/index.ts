import { defineComponent, h, nextTick, provide } from 'vue'
import DefaultTheme from 'vitepress/theme'
import { useData, type Theme } from 'vitepress'
import HomePitch from './components/HomePitch.vue'
import CostTimeline from './components/CostTimeline.vue'
import './style.css'

const Layout = defineComponent({
  setup () {
    const { isDark } = useData()

    // Cross-fade the dark/light switch instead of snapping, where the browser
    // can (View Transitions) and the reader has not asked for less motion.
    provide('toggle-appearance', async () => {
      const canFade = typeof document !== 'undefined' &&
        'startViewTransition' in document &&
        !window.matchMedia('(prefers-reduced-motion: reduce)').matches
      if (!canFade) {
        isDark.value = !isDark.value
        return
      }
      await document.startViewTransition(async () => {
        isDark.value = !isDark.value
        await nextTick()
      }).ready
    })

    // The pitch goes straight under the hero, before the feature cards.
    return () => h(DefaultTheme.Layout, null, {
      'home-hero-after': () => h(HomePitch),
    })
  },
})

export default {
  extends: DefaultTheme,
  Layout,
  enhanceApp ({ app }) {
    app.component('CostTimeline', CostTimeline)
  },
} satisfies Theme
