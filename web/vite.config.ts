/// <reference types="vitest/config" />
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { prerender } from './build/prerender-plugin.ts'

const apiTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), tailwindcss(), prerender()],
  build: {
    // Never inline fonts as data: URLs (small subsets otherwise are): the production CSP only allows fonts from our own origin.
    assetsInlineLimit: (file) => (/\.woff2?$/.test(file) ? false : undefined),
  },
  server: {
    // Tests only: src/routes.test.ts reads ../deploy/Caddyfile. The dev server keeps Vite's default (this directory only).
    fs: process.env.VITEST ? { allow: ['..'] } : undefined,
    port: 3000,
    proxy: {
      '/api/': apiTarget, // the trailing slash keeps app pages such as /api-keys in the app, as Caddy's /api/* does
      '/mcp': apiTarget, // the MCP server (exactly /mcp)
      '/oauth/': apiTarget, // OAuth endpoints for MCP clients (the consent page itself is /connect, in the app)
      '/.well-known/oauth-': apiTarget,
      '/healthz': apiTarget,
      '/readyz': apiTarget,
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    globals: false,
  },
})
