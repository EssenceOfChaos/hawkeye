import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import type { Plugin } from 'vite'
import { STATIC_PAGES } from '../src/seo.ts'
import { pageFile, renderPage, renderSitemap } from './render.ts'

/**
 * After the build, writes one HTML file per page with fixed address (dist/pages/<name>.html) from the built index.html, and
 * dist/sitemap.xml. The web server (deploy/Caddyfile) serves a page's file when it exists and the plain index.html otherwise (signed-in
 * pages with ids, status pages, 404s). index.html itself is left as it is: it must stay neutral, because it is the fallback for those.
 */
export function prerender(): Plugin {
  return {
    name: 'hawkeye-prerender',
    apply: 'build',
    writeBundle(options) {
      const dir = options.dir
      if (!dir) return
      const shell = readFileSync(join(dir, 'index.html'), 'utf8')
      for (const page of STATIC_PAGES) {
        const file = join(dir, pageFile(page.path))
        mkdirSync(dirname(file), { recursive: true })
        writeFileSync(file, renderPage(shell, page))
      }
      writeFileSync(join(dir, 'sitemap.xml'), renderSitemap())
    },
  }
}
