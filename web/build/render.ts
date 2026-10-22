import { canonicalUrl, DEFAULT_SHARE_IMAGE, SHARE_IMAGE_ALT, SITE_NAME, SITE_URL, STATIC_PAGES, type PageMeta } from '../src/seo.ts'

// Pure functions behind the prerender plugin: no file access, so tests can run them on a string.

const escapeAttr = (s: string) => s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
const escapeText = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

/** JSON for a <script> block: "<" is escaped so the data can never end the block. */
const scriptJson = (value: unknown) => JSON.stringify(value).replace(/</g, '\\u003c')

/** Where the prerendered file for a page lives under the output directory (and, minus ".html", where the web server looks for it). */
export function pageFile(path: string): string {
  return `pages${path === '/' ? '/home' : path}.html`
}

/**
 * Writes one page's head tags into the built index.html: its own title and description, and for an indexable page a canonical link,
 * Open Graph and Twitter tags (what link previews read) and any structured data; for the others, a robots "noindex".
 * Throws if the shell is missing the tags it replaces, so a change to index.html cannot silently produce pages without them.
 */
export function renderPage(shell: string, page: PageMeta): string {
  let html = shell
  const swap = (pattern: RegExp, replacement: string, what: string) => {
    if (!pattern.test(html)) throw new Error(`prerender: index.html has no ${what} to replace`)
    html = html.replace(pattern, () => replacement)
  }
  swap(/<title>[\s\S]*?<\/title>/, `<title>${escapeText(page.title)}</title>`, '<title>')
  swap(/<meta\s+name="description"[^>]*>/, `<meta name="description" content="${escapeAttr(page.description)}" />`, 'description meta tag')

  const extra: string[] = []
  if (page.index) {
    const url = canonicalUrl(page.path)
    const image = `${SITE_URL}${page.image ?? DEFAULT_SHARE_IMAGE}`
    extra.push(
      `<link rel="canonical" href="${escapeAttr(url)}" />`,
      `<meta property="og:type" content="website" />`,
      `<meta property="og:site_name" content="${escapeAttr(SITE_NAME)}" />`,
      `<meta property="og:title" content="${escapeAttr(page.title)}" />`,
      `<meta property="og:description" content="${escapeAttr(page.description)}" />`,
      `<meta property="og:url" content="${escapeAttr(url)}" />`,
      `<meta property="og:image" content="${escapeAttr(image)}" />`,
      `<meta property="og:image:width" content="1200" />`,
      `<meta property="og:image:height" content="630" />`,
      `<meta property="og:image:alt" content="${escapeAttr(SHARE_IMAGE_ALT)}" />`,
      `<meta name="twitter:card" content="summary_large_image" />`,
      `<meta name="twitter:title" content="${escapeAttr(page.title)}" />`,
      `<meta name="twitter:description" content="${escapeAttr(page.description)}" />`,
      `<meta name="twitter:image" content="${escapeAttr(image)}" />`,
    )
    if (page.jsonLd) extra.push(`<script type="application/ld+json">${scriptJson(page.jsonLd)}</script>`)
  } else {
    extra.push(`<meta name="robots" content="noindex" />`)
  }
  swap(/<\/head>/, `  ${extra.join('\n    ')}\n  </head>`, '</head>')
  return html
}

/** sitemap.xml: every indexable page with a fixed address. */
export function renderSitemap(): string {
  const urls = STATIC_PAGES.filter((p) => p.index).map((p) => `  <url><loc>${escapeText(canonicalUrl(p.path))}</loc></url>`)
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls.join('\n')}\n</urlset>\n`
}
