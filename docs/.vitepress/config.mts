import { defineConfig } from 'vitepress'

const repo = 'https://github.com/gurupras/agent-keepalive-manager'

// /download/ and /hub/ live on the same host but are not part of this site:
// nginx serves them. Links there must load the page, not go through the
// client-side router.
const offSite = /^(\/(download|hub)(\/|$)|https:\/\/agent-keepalive\.gurupras\.me\/(download|hub)(\/|$))/

export default defineConfig({
  title: 'agent-keepalive',
  description: 'Keeps the prompt caches of your Claude Code and Codex CLI sessions warm while you step away.',
  lang: 'en',
  cleanUrls: true,
  lastUpdated: true,
  ignoreDeadLinks: [offSite],
  head: [
    ['link', { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' }],
    ['meta', { name: 'theme-color', content: '#f05a28' }],
    ['meta', { property: 'og:title', content: 'agent-keepalive' }],
    ['meta', { property: 'og:description', content: 'Keeps the prompt caches of your Claude Code and Codex CLI sessions warm while you step away.' }],
    ['meta', { property: 'og:image', content: 'https://agent-keepalive.gurupras.me/screenshot.png' }],
  ],
  markdown: {
    config (md) {
      // The pages are also read on GitHub, so they link to the rest of the
      // repository with relative paths (../deploy/README.md). Point those at
      // GitHub, and send /download/ and /hub/ links past the router.
      md.core.ruler.push('repo-links', state => {
        for (const block of state.tokens) {
          for (const t of block.children ?? []) {
            if (t.type !== 'link_open') continue
            const href = t.attrGet('href') ?? ''
            if (href.startsWith('../')) {
              const path = href.replace(/^(\.\.\/)+/, '')
              t.attrSet('href', `${repo}/${path.endsWith('/') ? 'tree' : 'blob'}/main/${path}`)
            } else if (offSite.test(href)) {
              t.attrSet('target', '_self')
            }
          }
        }
      })
    },
  },
  themeConfig: {
    logo: '/favicon.svg',
    nav: [
      { text: 'Guide', link: '/getting-started', activeMatch: '^/(getting-started|install|using|cli|agents|metrics)' },
      { text: 'Reference', link: '/architecture', activeMatch: '^/(architecture|api|release-artifacts|package-repositories)' },
      { text: 'Self-hosting', link: '/self-hosting', activeMatch: '^/(self-hosting|releasing)' },
      { text: 'Downloads', link: '/download/', target: '_self' },
    ],
    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'Getting started', link: '/getting-started' },
          { text: 'Installing', link: '/install' },
          { text: 'Using the UI', link: '/using' },
          { text: 'Command line', link: '/cli' },
          { text: 'Agents and swarms (MCP)', link: '/agents' },
          { text: 'Usage reports and privacy', link: '/metrics' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'How it works', link: '/architecture' },
          { text: 'Local API', link: '/api' },
          { text: 'Package repositories', link: '/package-repositories' },
          { text: 'Release artifacts and updates', link: '/release-artifacts' },
        ],
      },
      {
        text: 'Running your own',
        items: [
          { text: 'Self-hosting and organisation builds', link: '/self-hosting' },
          { text: 'Releasing', link: '/releasing' },
        ],
      },
    ],
    socialLinks: [{ icon: 'github', link: repo }],
    search: { provider: 'local' },
    editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub' },
    outline: { level: [2, 3] },
    footer: {
      message: 'Released under the MIT License.',
      copyright: `<a href="${repo}">Source on GitHub</a> · <a href="/download/" target="_self">Downloads</a>`,
    },
  },
})
