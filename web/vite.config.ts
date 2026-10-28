import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { join, resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { iconCollections } from './build/icons.ts'

// `npm run dev` proxies api/ to a running server (KEEPALIVE_API, default the
// usual port). The server puts its API token into the page it serves; the
// dev server does the same with the token from KEEPALIVE_TOKEN or the data
// dir's token file.
const backend = process.env.KEEPALIVE_API ?? 'http://127.0.0.1:4344'

function devToken (): string {
  if (process.env.KEEPALIVE_TOKEN) return process.env.KEEPALIVE_TOKEN
  const dirs = [
    process.env.AGENT_KEEPALIVE_DATA_DIR,
    process.env.XDG_STATE_HOME && join(process.env.XDG_STATE_HOME, 'agent-keepalive'),
    join(homedir(), '.local', 'state', 'agent-keepalive'),
    join(homedir(), 'Library', 'Application Support', 'agent-keepalive'),
    process.env.LOCALAPPDATA && join(process.env.LOCALAPPDATA, 'agent-keepalive'),
  ]
  for (const d of dirs) {
    const f = d && join(d, 'token')
    if (f && existsSync(f)) return readFileSync(f, 'utf8').trim()
  }
  return ''
}

/** devTokenMeta adds <meta name="ka-token"> to the page in `vite dev` only. */
function devTokenMeta (): Plugin {
  return {
    name: 'agent-keepalive:dev-token',
    apply: 'serve',
    transformIndexHtml () {
      const token = devToken()
      return token ? [{ tag: 'meta', attrs: { name: 'ka-token', content: token }, injectTo: 'head' }] : []
    },
  }
}

/** keepGitkeep puts dist/.gitkeep back after Vite empties the output directory. */
function keepGitkeep (): Plugin {
  return {
    name: 'agent-keepalive:keep-gitkeep',
    apply: 'build',
    closeBundle () {
      writeFileSync(resolve(import.meta.dirname, 'dist', '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  // Relative base: the app is served under an arbitrary path prefix.
  base: './',
  plugins: [vue(), tailwindcss(), iconCollections(), devTokenMeta(), keepGitkeep()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // No inline polyfill script, no data: URLs: the page runs under
    // script-src 'self'; style-src 'self'.
    modulePreload: { polyfill: false },
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 800,
  },
  server: {
    proxy: {
      '/api': {
        target: backend,
        changeOrigin: true,
        ws: true,
        // The server refuses cross-origin writes and WebSocket upgrades;
        // present proxied requests as same-origin.
        configure: proxy => {
          proxy.on('proxyReq', req => {
            if (req.getHeader('origin')) req.setHeader('origin', backend)
          })
          proxy.on('proxyReqWs', req => {
            if (req.getHeader('origin')) req.setHeader('origin', backend)
          })
        },
      },
    },
  },
})
