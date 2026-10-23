import { describe, expect, it } from 'vitest'
import app from './App.tsx?raw'
import caddy from '../../deploy/Caddyfile?raw'
import indexHtml from '../index.html?raw'
import robots from '../public/robots.txt?raw'
import { DEFAULT_SHARE_IMAGE, HOME_DESCRIPTION, HOME_TITLE, PAGES, STATIC_PAGES } from './seo'

// The preview images in public/share. Vite lists the files that exist; a missing one is a link preview with a broken picture.
const shareImages = Object.keys(import.meta.glob('../public/share/*.jpg')).map((f) => f.replace('../public', ''))

// The page map (seo.ts) drives each page's title, the prerendered HTML and the sitemap. These tests keep it in step with the app's routes,
// the web server and the shell, so a new page cannot ship without a title, and a prerendered page cannot be unreachable.

const appRoutes = [...app.matchAll(/<Route[^>]*\spath="([^"]+)"/g)].map((m) => m[1]).filter((p) => p !== '*')
const caddyPattern = new RegExp(caddy.match(/@app path_regexp app (\S+)/)![1].replace('(?i)', ''), 'i')
// A status page names itself (it has the organisation's title), so it is the one route that is not in the map.
const selfTitled = ['/status/:slug']

describe('page map', () => {
  it('has an entry for every route in App.tsx, and no entry for a route that does not exist', () => {
    expect(appRoutes.length).toBeGreaterThan(15)
    expect(PAGES.map((p) => p.path).sort()).toEqual(appRoutes.filter((r) => !selfTitled.includes(r)).sort())
  })

  it('gives every page its own title and a description of a sensible length', () => {
    const titles = PAGES.map((p) => p.title)
    expect(new Set(titles).size, 'titles must be unique').toBe(titles.length)
    for (const p of PAGES) {
      expect(p.title.length, p.path).toBeGreaterThan(0)
      expect(p.title.length, `${p.path} title is cut off in search results`).toBeLessThanOrEqual(60)
      expect(p.description.length, p.path).toBeGreaterThan(0)
      expect(p.description.length, `${p.path} description is cut off in search results`).toBeLessThanOrEqual(160)
    }
  })

  it('only offers search engines the public pages: nothing behind a login, and the pages robots.txt blocks are never indexable', () => {
    const disallowed = [...robots.matchAll(/^Disallow:\s*(\S+)/gm)].map((m) => m[1])
    for (const p of PAGES.filter((p) => p.index)) {
      expect(disallowed.some((d) => p.path.startsWith(d)), `${p.path} is indexable but robots.txt blocks it`).toBe(false)
    }
    expect(PAGES.filter((p) => p.index).map((p) => p.path)).toEqual(['/', '/tools', '/tools/is-website-down', '/tools/ssl-certificate-checker', '/tools/uptime-sla-calculator', '/tools/downtime-cost-calculator', '/contact', '/privacy', '/terms'])
  })

  it('reaches every prerendered page through the web server (a page the Caddyfile calls unknown would 404)', () => {
    for (const p of STATIC_PAGES) expect(p.path === '/' || caddyPattern.test(p.path), p.path).toBe(true)
  })

  it('keeps the shell index.html in step with the home entry, and neutral (no canonical or robots tag that other routes would inherit)', () => {
    expect(indexHtml).toContain(`<title>${HOME_TITLE.replaceAll('&', '&amp;')}</title>`)
    expect(indexHtml).toContain(`content="${HOME_DESCRIPTION}"`)
    expect(indexHtml).not.toMatch(/rel="canonical"|name="robots"/)
  })
})

describe('preview images', () => {
  it('exist for every indexable page, and the default one exists', () => {
    expect(shareImages).toContain(DEFAULT_SHARE_IMAGE)
    for (const p of PAGES.filter((p) => p.index && p.image)) expect(shareImages, `${p.path} names ${p.image}`).toContain(p.image)
  })

  it('give every tool page its own image', () => {
    for (const p of PAGES.filter((p) => p.path.startsWith('/tools'))) expect(p.image, p.path).toBeDefined()
  })
})

describe('web server', () => {
  it('serves the prerendered file for a page and falls back to the shell', () => {
    expect(caddy).toContain('try_files /pages{path}.html /index.html')
    expect(caddy).toContain('rewrite * /pages/home.html')
  })
})
