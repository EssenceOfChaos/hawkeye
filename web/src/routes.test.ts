import { describe, expect, it } from 'vitest'
import app from './App.tsx?raw'
import caddy from '../../deploy/Caddyfile?raw'

// The web server (deploy/Caddyfile) must know which addresses are real pages: it answers everything else with HTTP 404. This test keeps
// its list of pages in step with the routes in App.tsx, so adding a page without updating the Caddyfile fails here rather than in production.

const match = caddy.match(/@app path_regexp app (\S+)/)
const knownPage = new RegExp(match![1].replace('(?i)', ''), 'i')

const routes = [...app.matchAll(/<Route[^>]*\spath="([^"]+)"/g)].map((m) => m[1]).filter((p) => p !== '*')

describe('page routes known to the web server', () => {
  it('finds the routes in App.tsx and the pattern in the Caddyfile', () => {
    expect(match).not.toBeNull()
    expect(routes.length).toBeGreaterThan(15)
  })

  it.each(routes)('serves %s as a page (not a 404)', (route) => {
    const path = route.replace(/:\w+/g, 'abc')
    expect(knownPage.test(path), `${path} is an app route but the Caddyfile @app pattern does not match it`).toBe(true)
  })

  it.each(['/nope', '/dashboard/extra', '/monitors/a/b/c', '/status/a/b', '/login.php', '/.env', '/wp-admin', '/api'])('treats %s as unknown', (path) => {
    expect(knownPage.test(path)).toBe(false)
  })
})
