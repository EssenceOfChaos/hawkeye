import { useEffect } from 'react'
import { useLocation, useNavigationType } from 'react-router'

/**
 * Starts each page at the top. In a single-page app the browser keeps the scroll position when the address changes, so a link in the footer
 * of a long page would open the next page already scrolled to its bottom, below its heading. Going back or forward is left to the browser,
 * which restores where the visitor was.
 *
 * A link to a #section of another page (the About page's "Explore Pricing" opens /#pricing-h) changes the address without the browser scrolling,
 * so the section is scrolled into view here. A plain <a href="#section"> on the same page never reaches the router and scrolls by itself.
 */
export function useScrollToTopOnNavigation() {
  const { pathname, hash } = useLocation()
  const type = useNavigationType()
  useEffect(() => {
    if (type === 'POP') return
    if (hash) {
      let id = hash.slice(1)
      try {
        id = decodeURIComponent(id)
      } catch {
        // a malformed escape such as "#%": look the id up as written
      }
      document.getElementById(id)?.scrollIntoView?.()
      return
    }
    window.scrollTo(0, 0)
  }, [pathname, hash, type])
}
