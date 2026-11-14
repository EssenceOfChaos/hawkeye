import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from '../api'
import { Toggle } from './Toggle'

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

/**
 * The owner's switch for AI-drafted incident updates, with what it sends where. Hidden when the server has no model configured; on Free it says which
 * plans include it.
 */
export function AIDraftsSetting({ state, onChange }: { state: AIDraftsState; onChange: (s: AIDraftsState) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!state.available) return null
  if (!state.included) {
    return (
      <p className="hint">
        On Pro and Agency, HawkEye can write a first draft of each incident update for you to edit. <Link to="/billing">See plans</Link>
      </p>
    )
  }
  async function toggle(on: boolean) {
    setBusy(true)
    setError(null)
    try {
      onChange(await api<AIDraftsState>('PUT', '/ai-drafts', { enabled: on }))
      return true
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
      return false
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="card" aria-labelledby="ai-drafts-h">
      <h2 id="ai-drafts-h" className="sr-only">AI-drafted updates</h2>
      <Toggle checked={state.enabled} busy={busy} onToggle={toggle}>Draft incident updates with AI</Toggle>
      <p className="hint">
        Adds a <strong>Draft with AI</strong> button to each update. HawkEye sends {state.model} the incident’s details: the monitor’s name, when the
        problem started and ended, the kind of failure, status codes, check locations and the updates already posted. Never the monitor’s address or
        anything from its responses. Drafts aren’t stored, and nothing is posted until you post it.
      </p>
      {error && <small className="error">{error}</small>}
    </section>
  )
}
