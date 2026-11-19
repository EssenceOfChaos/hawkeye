import { useLayoutEffect, useRef, type ElementType, type ReactNode } from 'react'
import { inScope, prefersReducedMotion, revealOnScroll } from '../motion'

/**
 * A container whose `[data-reveal]` children fade in as it scrolls into view. Without animation support the children are simply visible.
 *
 * Items can arrive after it mounts: the "Checks from several locations" card and its FAQ answer appear only once the plans load from the API.
 * Hidden items that nothing animates stay invisible, so when the number of `[data-reveal]` children changes the fade-in is set up again
 * for all of them (found 2026-11-19: the card was on the page at opacity 0).
 */
export function Reveal({ as: Tag = 'div', className, children }: { as?: ElementType; className?: string; children: ReactNode }) {
  const ref = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || prefersReducedMotion()) return
    el.classList.add('reveal-armed')
    const count = () => el.querySelectorAll('[data-reveal]').length
    let seen = count()
    let stop = inScope(el, () => revealOnScroll(el))
    const watch = new MutationObserver(() => {
      if (count() === seen) return
      seen = count()
      stop()
      stop = inScope(el, () => revealOnScroll(el))
    })
    watch.observe(el, { childList: true, subtree: true })
    return () => {
      watch.disconnect()
      stop()
      el.classList.remove('reveal-armed')
    }
  }, [])
  return (
    <Tag ref={ref} className={className}>
      {children}
    </Tag>
  )
}
