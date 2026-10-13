import { useLayoutEffect, useRef, type ElementType, type ReactNode } from 'react'
import { inScope, prefersReducedMotion, revealOnScroll } from '../motion'

/**
 * A container whose `[data-reveal]` children fade in as it scrolls into view. It sets itself up when it mounts, so content that
 * appears later (for example plans loaded from the API) animates too. Without animation support the children are simply visible.
 */
export function Reveal({ as: Tag = 'div', className, children }: { as?: ElementType; className?: string; children: ReactNode }) {
  const ref = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || prefersReducedMotion()) return
    el.classList.add('reveal-armed')
    const stop = inScope(el, () => revealOnScroll(el))
    return () => {
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
