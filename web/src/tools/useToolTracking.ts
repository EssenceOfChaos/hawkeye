import { useRef } from 'react'
import { trackEvent } from '../analytics'

/** Analytics for a free tool: one tool_use the first time the visitor changes an input, so visits, uses and sign-up clicks can be compared. */
export function useToolUse(tool: string) {
  const used = useRef(false)
  return () => {
    if (used.current) return
    used.current = true
    trackEvent('tool_use', { tool })
  }
}
