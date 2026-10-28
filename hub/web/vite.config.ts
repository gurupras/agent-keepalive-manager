import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// `npm run dev` proxies v1/ and healthz to a running hub (HUB_API, default
// http://127.0.0.1:8080).
const backend = process.env.HUB_API ?? 'http://127.0.0.1:8080'

/** keepGitkeep puts dist/.gitkeep back after Vite empties the output directory. */
function keepGitkeep (): Plugin {
  return {
    name: 'agent-keepalive-hub:keep-gitkeep',
    apply: 'build',
    closeBundle () {
      writeFileSync(resolve(import.meta.dirname, 'dist', '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  // Relative base: the hub can be served under any --base-path.
  base: './',
  plugins: [vue(), tailwindcss(), keepGitkeep()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // No inline polyfill script, no data: URLs: the page runs under
    // script-src 'self'; style-src 'self'.
    modulePreload: { polyfill: false },
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      '/v1': { target: backend, changeOrigin: true },
      '/healthz': { target: backend, changeOrigin: true },
    },
  },
})
