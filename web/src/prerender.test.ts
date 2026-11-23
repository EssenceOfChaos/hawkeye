import { describe, expect, it } from 'vitest'
import { PAGES, STATIC_PAGES } from './seo'
import { pageFile, renderPage, renderSitemap } from '../build/render.ts'

const shell = `<!doctype html>
<html lang="en">
  <head>
    <title>Shell &amp; title</title>
    <meta name="description" content="Shell description" />
  </head>
  <body><div id="root"></div></body>
</html>`

describe('renderPage', () => {
  it('puts what HawkEye is, and links to the Privacy Policy and Terms, in a public page for readers without JavaScript', () => {
    const html = renderPage(shell, PAGES.find((p) => p.path === '/')!)
    const body = html.slice(html.indexOf('<div id="root">'))
    expect(body).toContain('<h1>HawkEye — Website Uptime Monitoring &amp; Alerts</h1>')
    expect(body).toContain('watches your websites, APIs, cron jobs and DNS records')
    expect(body).toContain('<a href="/privacy">Privacy Policy</a>')
    expect(body).toContain('<a href="/terms">Terms of Service</a>')
    // Other public pages get their own heading, without the site name.
    expect(renderPage(shell, PAGES.find((p) => p.path === '/privacy')!)).toContain('<h1>Privacy Policy</h1>')
  })

  it('leaves a page that is not for search results empty, so signed-in pages don’t flash text before the app starts', () => {
    expect(renderPage(shell, PAGES.find((p) => p.path === '/monitoring')!)).toContain('<div id="root"></div>')
  })

  const home = PAGES.find((p) => p.path === '/')!
  const hidden = PAGES.find((p) => p.path === '/monitoring')!

  it('gives an indexable page its title, description, canonical, share tags and structured data', () => {
    const html = renderPage(shell, home)
    expect(html).toContain('<title>HawkEye — Website Uptime Monitoring &amp; Alerts</title>')
    expect(html).toContain(`<meta name="description" content="${home.description}" />`)
    expect(html).toContain('<link rel="canonical" href="https://hawkeye.observer/" />')
    expect(html).toContain('<meta property="og:url" content="https://hawkeye.observer/" />')
    expect(html).toContain('<meta name="twitter:card" content="summary_large_image" />')
    expect(html).toContain('<meta property="og:image" content="https://hawkeye.observer/share/home.jpg" />')
    expect(html).toContain('<meta name="twitter:image" content="https://hawkeye.observer/share/home.jpg" />')
    expect(html).toContain('<script type="application/ld+json">')
    expect(html).not.toContain('Shell')
    expect(html).not.toContain('noindex')
  })

  it('uses a page’s own preview image, and the home image for pages without one', () => {
    expect(renderPage(shell, PAGES.find((p) => p.path === '/tools/ssl-certificate-checker')!)).toContain('content="https://hawkeye.observer/share/ssl-certificate-checker.jpg"')
    expect(renderPage(shell, PAGES.find((p) => p.path === '/contact')!)).toContain('content="https://hawkeye.observer/share/home.jpg"')
  })

  it('marks a page that is not for search results as noindex, with no canonical', () => {
    const html = renderPage(shell, hidden)
    expect(html).toContain('<meta name="robots" content="noindex" />')
    expect(html).not.toContain('canonical')
    expect(html).not.toContain('og:')
  })

  it('escapes text so a title or description cannot break out of its tag', () => {
    const html = renderPage(shell, { path: '/x', title: 'A "quoted" <b> & more', description: 'say "hi" <script>', index: true, jsonLd: { name: '</script><b>' } })
    expect(html).toContain('<title>A "quoted" &lt;b&gt; &amp; more</title>')
    expect(html).toContain('content="say &quot;hi&quot; &lt;script&gt;"')
    expect(html.match(/<\/script>/g)).toHaveLength(1) // only the structured-data block's own end tag
  })

  it('fails loudly when the shell has no tag to replace', () => {
    expect(() => renderPage('<html><head></head></html>', home)).toThrow(/<title>/)
    expect(() => renderPage('<html><head><title>x</title></head></html>', home)).toThrow(/description/)
  })
})

describe('pageFile', () => {
  it('maps addresses to files the Caddyfile looks for (/pages<path>.html, and home for /)', () => {
    expect(pageFile('/')).toBe('pages/home.html')
    expect(pageFile('/contact')).toBe('pages/contact.html')
    expect(pageFile('/monitors/new')).toBe('pages/monitors/new.html')
  })
})

describe('renderSitemap', () => {
  it('lists every indexable page and nothing else', () => {
    const xml = renderSitemap()
    const listed = [...xml.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1])
    expect(listed).toEqual(STATIC_PAGES.filter((p) => p.index).map((p) => (p.path === '/' ? 'https://hawkeye.observer/' : `https://hawkeye.observer${p.path}`)))
    expect(xml).not.toMatch(/observer\/(login|register|dashboard|monitoring)</)
  })
})
