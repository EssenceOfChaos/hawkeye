import { useEffect } from 'react'
import { matchPath } from 'react-router'
import { canonicalUrl, PAGES, type PageMeta } from './seo'

/** The entry in the page map for a browser path, or undefined for addresses that set their own head (status pages, the 404 page). */
export function findPage(pathname: string): PageMeta | undefined {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  return PAGES.find((p) => matchPath({ path: p.path, end: true }, path))
}

function headTag<K extends 'meta' | 'link'>(tag: K, selector: string, attrs: Record<string, string>): HTMLElementTagNameMap[K] {
  let el = document.head.querySelector<HTMLElementTagNameMap[K]>(selector)
  if (!el) {
    el = document.createElement(tag)
    document.head.appendChild(el)
  }
  for (const [name, value] of Object.entries(attrs)) el.setAttribute(name, value)
  return el
}

/**
 * Makes the document head describe the current page: title, description, and either a canonical link (indexable pages) or a robots
 * "noindex" (the rest). It sets the same tags the build writes into the prerendered HTML, so a page looks the same whether it was loaded
 * directly or reached by clicking. Leaving the page removes the robots and canonical tags, so the next page starts clean; the next
 * page sets the title.
 */
export function usePageMeta(page: PageMeta | undefined) {
  const { path, title, description, index } = page ?? { path: '', title: '', description: '', index: false }
  const active = page !== undefined
  useEffect(() => {
    if (!active) return
    document.title = title
    headTag('meta', 'meta[name="description"]', { name: 'description', content: description })
    if (index) {
      headTag('link', 'link[rel="canonical"]', { rel: 'canonical', href: canonicalUrl(path) })
      document.head.querySelector('meta[name="robots"]')?.remove()
    } else {
      headTag('meta', 'meta[name="robots"]', { name: 'robots', content: 'noindex' })
      document.head.querySelector('link[rel="canonical"]')?.remove()
    }
    return () => {
      document.head.querySelector('meta[name="robots"]')?.remove()
      document.head.querySelector('link[rel="canonical"]')?.remove()
    }
  }, [active, path, title, description, index])
}
