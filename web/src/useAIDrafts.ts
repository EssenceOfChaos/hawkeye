import { useEffect, useState } from 'react'
import { api } from './api'

/** What GET /ai-drafts says: drafting exists on this server, the plan includes it, and the owner turned it on. */
export interface AIDraftsState {
  available: boolean
  included: boolean
  enabled: boolean
  model: string
}

/** Loads the organization's AI drafts setting. Null until loaded, and stays null if it can't be (the feature then just isn't offered). */
export function useAIDrafts(): [AIDraftsState | null, (s: AIDraftsState) => void] {
  const [state, setState] = useState<AIDraftsState | null>(null)
  useEffect(() => {
    let live = true
    api<AIDraftsState>('GET', '/ai-drafts').then((r) => live && setState(r), () => {})
    return () => {
      live = false
    }
  }, [])
  return [state, setState]
}
