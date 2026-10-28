import './composables/theme'
import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { registerIcons } from './icons'
import { start } from './api/store'
import './style.css'

registerIcons()
createApp(App).use(router).mount('#app')
start()
