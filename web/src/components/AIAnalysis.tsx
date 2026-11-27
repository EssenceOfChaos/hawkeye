import { type CSSProperties, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type IncidentAnalysis } from '../api'
import type { AIAnalysisState } from '../useAIAnalysis'
import { Toggle } from './Toggle'

/**
 * The owner's switch for AI incident analysis, with what it sends where and the month's usage. Hidden when the server has no model configured.
 */
export function AIAnalysisSetting({ state, onChange }: { state: AIAnalysisState; onChange: (s: AIAnalysisState) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!state.available) return null
  if (!state.included) {
    return (
      <p className="hint">
        HawkEye can write the likely cause of each outage with AI. <Link to="/billing">See plans</Link>
      </p>
    )
  }
  async function toggle(on: boolean) {
    setBusy(true)
    setError(null)
    try {
      onChange(await api<AIAnalysisState>('PUT', '/ai-analysis', { enabled: on }))
      return true
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
      return false
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="card" aria-labelledby="ai-analysis-h">
      <h2 id="ai-analysis-h" className="sr-only">AI incident analysis</h2>
      <Toggle checked={state.enabled} busy={busy} onToggle={toggle}>Find the likely cause of outages with AI</Toggle>
      <p className="hint">
        When an outage of a website, API or multi-step check is confirmed, HawkEye sends {state.model} what its check saw: the status code, timings,
        response headers, what changed since the last good check and what each location saw. Never the monitor’s name or address, its credentials,
        or the response itself.
      </p>
      {state.enabled && <AnalysisUsage state={state} upgrade />}
      {error && <small className="error">{error}</small>}
    </section>
  )
}

/** The first day of next month, when the calendar-month (UTC) allowance starts again, such as "December 1". */
function resetDay(now = new Date()): string {
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1)).toLocaleDateString('en-US', { month: 'long', day: 'numeric', timeZone: 'UTC' })
}

/**
 * How much of this month's AI analysis allowance is used, as a ring (daisyUI radial progress) with the count beside it: gold, amber from 80% and red
 * when used up, with when it starts again. Used on the Incidents page (the owner's switch) and the Billing page (the plan's usage).
 */
export function AnalysisUsage({ state, upgrade = false }: { state: Pick<AIAnalysisState, 'used' | 'limit'>; upgrade?: boolean }) {
  const { used, limit } = state
  if (limit <= 0) return null
  const pct = Math.min(100, Math.round((used / limit) * 100))
  const full = used >= limit
  const tone = full ? 'full' : pct >= 80 ? 'near' : 'ok'
  // daisyUI's variables carry our d- prefix too, like its class names.
  const style = { '--d-value': pct, '--d-size': '3.25rem', '--d-thickness': '.35rem' } as CSSProperties
  return (
    <div className={`analysis-usage ${tone}`}>
      <div className="d-radial-progress" style={style} role="progressbar" aria-label="AI analyses used this month" aria-valuemin={0}
        aria-valuemax={limit} aria-valuenow={Math.min(used, limit)} aria-valuetext={`${used} of ${limit} used`}>
        <span aria-hidden="true">{pct}%</span>
      </div>
      <p>
        <strong>{used.toLocaleString('en-US')} of {limit.toLocaleString('en-US')}</strong> AI incident analyses used this month
        <br />
        <span className="hint">
          {full ? <>All used: new outages aren’t analyzed until {resetDay()}.</> : <>{(limit - used).toLocaleString('en-US')} left · starts again {resetDay()}</>}
          {full && upgrade && <> <Link to="/billing">See plans</Link></>}
        </span>
      </p>
    </div>
  )
}

const CATEGORY: Record<string, string> = {
  origin_server: 'Origin server', cdn_or_proxy: 'CDN or proxy', dns: 'DNS', tls_certificate: 'TLS certificate', network: 'Network',
  slow_response: 'Slow response', application_error: 'Application error', content_changed: 'Content changed', authentication: 'Authentication',
  rate_limited: 'Rate limited', unknown: 'Unclear',
}

const ENDED: Record<string, string> = {
  declined: 'An analysis couldn’t be written for this incident.',
  failed: 'The analysis couldn’t be written because the AI service wasn’t available. Try again later.',
  over_limit: 'This month’s AI analyses were all used, so this incident wasn’t analyzed.',
  off: 'AI analysis was turned off before this incident was analyzed.',
}

/**
 * The likely cause of an incident's outage, written by AI from its failure snapshot, labeled as possibly wrong. Polls while the analysis is being
 * written. With analysis turned on, offers to write one (or a new one), which counts against the month's allowance. The text comes from a model
 * that read data from the monitored site, so it is only ever shown as text.
 */
export function IncidentAnalysisPanel({ incidentID, hasAnalysis, setting, onRequested }: {
  incidentID: string
  hasAnalysis: boolean
  setting: AIAnalysisState | null
  onRequested: () => void
}) {
  const [analysis, setAnalysis] = useState<IncidentAnalysis | null | undefined>(hasAnalysis ? undefined : null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const waiting = analysis?.status === 'pending' || analysis?.status === 'running'

  useEffect(() => {
    if (!hasAnalysis && analysis === null) return
    if (analysis !== undefined && !waiting) return
    let live = true
    const load = () =>
      api<{ analysis: IncidentAnalysis }>('GET', `/incidents/${incidentID}/analysis`)
        .then((r) => { if (live) setAnalysis(r.analysis) })
        .catch((e: unknown) => {
          if (!live) return
          if (e instanceof ApiError && e.status === 404) setAnalysis(null)
          else setError(e instanceof ApiError ? e.message : 'Could not load the analysis.')
        })
    // Load at once; while it is being written, check again every few seconds.
    const timer = analysis === undefined ? (void load(), undefined) : setTimeout(() => void load(), 3000)
    return () => {
      live = false
      if (timer) clearTimeout(timer)
    }
  }, [incidentID, hasAnalysis, analysis, waiting])

  async function analyze() {
    setBusy(true)
    setError(null)
    try {
      setAnalysis((await api<{ analysis: IncidentAnalysis }>('POST', `/incidents/${incidentID}/analysis`)).analysis)
      onRequested()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  const canAsk = Boolean(setting?.available && setting.included && setting.enabled)
  if (analysis === undefined) return <p className="hint" role="status">Loading the analysis…</p>
  if (analysis === null && !canAsk) return null

  const left = setting ? Math.max(0, setting.limit - setting.used) : 0
  const button = canAsk && !waiting && (
    <p>
      <button type="button" onClick={() => void analyze()} disabled={busy || left === 0}>
        {busy ? 'Asking…' : analysis ? 'Analyze again' : 'Find the likely cause with AI'}
      </button>{' '}
      <span className="hint">{left === 0 ? 'This month’s analyses are all used.' : `Uses one of the ${left.toLocaleString('en-US')} analyses left this month.`}</span>
    </p>
  )

  return (
    <section className="incident-analysis" aria-labelledby={`analysis-${incidentID}`} aria-live="polite">
      <h3 id={`analysis-${incidentID}`}>
        Likely cause <span className="soon">AI analysis · may be wrong</span>
      </h3>
      {waiting && <p className="hint" role="status"><span className="d-loading d-loading-spinner d-loading-xs" aria-hidden="true" /> Writing the analysis…</p>}
      {analysis?.status === 'done' && (
        <>
          <p className="analysis-cause">
            {analysis.category && analysis.category !== 'unknown' && <strong>{CATEGORY[analysis.category] ?? analysis.category}: </strong>}
            {analysis.likely_cause}
          </p>
          {analysis.evidence.length > 0 && (
            <ul className="snapshot-changes">{analysis.evidence.map((e, i) => <li key={i}>{e}</li>)}</ul>
          )}
          {analysis.next_step && <p><strong>Next step:</strong> {analysis.next_step}</p>}
          <p className="hint">
            Confidence: {analysis.confidence}. Written by {setting?.model ?? 'AI'} from what HawkEye saw
            {analysis.finished_at && <> on <time dateTime={analysis.finished_at}>{new Date(analysis.finished_at).toLocaleString()}</time></>}.
          </p>
        </>
      )}
      {analysis && ENDED[analysis.status] && <p className="hint">{ENDED[analysis.status]}</p>}
      {analysis === null && <p className="hint">HawkEye can read what its check saw and suggest the likely cause.</p>}
      {button}
      {error && <small className="error">{error}</small>}
    </section>
  )
}
