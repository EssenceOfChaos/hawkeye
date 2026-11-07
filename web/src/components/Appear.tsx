import { useLayoutEffect, useRef, type ReactNode } from 'react'
import { appear, prefersReducedMotion } from '../motion'

/** Fades its children in when it mounts, to draw the eye to something that just changed. Set `play` to false to show them without motion. */
export function Appear({ play = true, className, children }: { play?: boolean; className?: string; children: ReactNode }) {
  const root = useRef<HTMLDivElement>(null)
  const shouldPlay = useRef(play) // decided once, on mount: a later change of `play` must not replay it
  useLayoutEffect(() => {
    if (!root.current || !shouldPlay.current || prefersReducedMotion()) return
    return appear(root.current)
  }, [])
  return <div ref={root} className={className}>{children}</div>
}
