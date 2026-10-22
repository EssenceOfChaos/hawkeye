import { useEffect } from 'react'
import { useLocation, useNavigationType } from 'react-router'

/**
 * Starts each page at the top. In a single-page app the browser keeps the scroll position when the address changes, so a link in the footer
 * of a long page would open the next page already scrolled to its bottom, below its heading. Going back or forward is left to the browser,
 * which restores where the visitor was, and a link to a #section scrolls to it by itself.
 */
export function useScrollToTopOnNavigation() {
  const { pathname, hash } = useLocation()
  const type = useNavigationType()
  useEffect(() => {
    if (type === 'POP' || hash) return
    window.scrollTo(0, 0)
  }, [pathname, hash, type])
}
