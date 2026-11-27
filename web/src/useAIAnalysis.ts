import { useCallback, useEffect, useState } from 'react'
import { api } from './api'

/** What GET /ai-analysis says: analysis exists on this server, the plan includes it, the owner turned it on, and the month's usage. */
export interface AIAnalysisState {
  available: boolean
  included: boolean
  enabled: boolean
  /** Analyses a calendar month (UTC) on the plan. */
  limit: number
  /** Analyses used this month. */
  used: number
  model: string
}

/**
 * Loads the organization's AI analysis setting. Null until loaded, and stays null if it can't be (the feature then just isn't offered). reload
 * fetches it again, after an analysis has been asked for, so the usage shown is current.
 */
export function useAIAnalysis(): [AIAnalysisState | null, (s: AIAnalysisState) => void, () => void] {
  const [state, setState] = useState<AIAnalysisState | null>(null)
  const reload = useCallback(() => {
    api<AIAnalysisState>('GET', '/ai-analysis').then(setState, () => {})
  }, [])
  useEffect(() => {
    let live = true
    api<AIAnalysisState>('GET', '/ai-analysis').then((r) => live && setState(r), () => {})
    return () => {
      live = false
    }
  }, [])
  return [state, setState, reload]
}
