import { useEffect, useState, type SubmitEvent } from 'react'
import { Link, useSearchParams } from 'react-router'
import { api, ApiError, type Usage } from '../api'
import { Alert } from '../components/Alert'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

/** One monitor POST /monitors/suggestions proposes. `monitor` holds exactly the fields POST /monitors takes. */
export interface MonitorSuggestion {
  kind: 'website' | 'health' | 'api' | 'mail'
  reason: string
  /** Preselected. False when the monitor would probably report a working site as down. */
  recommended: boolean
  already_monitored: boolean
  monitor: { type: 'http' | 'dns'; name: string; url: string; dns_record_type?: string }
}

interface Outcome {
  name: string
  id?: string
  error?: string
  upgrade?: boolean
}

const KIND_LABELS: Record<MonitorSuggestion['kind'], string> = {
  website: 'Website',
  health: 'Health endpoint',
  api: 'API',
  mail: 'Email (MX records)',
}

/** What the monitor checks, as a person reads it. */
function target(s: MonitorSuggestion) {
  return s.monitor.type === 'dns' ? `${s.monitor.dns_record_type ?? 'A'} records of ${s.monitor.url}` : s.monitor.url
}

/**
 * "Set up from your domain": the customer names their site, HawkEye looks at it from outside and proposes monitors, and the customer picks which to add.
 * Nothing is created until they confirm, and each pick is created through POST /monitors, so plan limits and validation are those of adding one by hand.
 */
export function SetupPage() {
  const [params] = useSearchParams()
  const [domain, setDomain] = useState(params.get('domain') ?? '')
  const [usage, setUsage] = useState<Usage | null>(null)
  const [suggestions, setSuggestions] = useState<MonitorSuggestion[] | null>(null)
  const [looked, setLooked] = useState('')
  const [picked, setPicked] = useState<number[]>([])
  const [busy, setBusy] = useState<'looking' | 'adding' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<string | undefined>()
  const [outcomes, setOutcomes] = useState<Outcome[] | null>(null)

  useEffect(() => {
    api<{ usage: Usage }>('GET', '/usage').then((r) => setUsage(r.usage), () => setUsage(null))
  }, [])

  const room = usage ? Math.max(0, usage.monitor_limit - usage.monitors_used) : Infinity

  async function look(e: SubmitEvent) {
    e.preventDefault()
    setBusy('looking')
    setError(null)
    setFieldError(undefined)
    setOutcomes(null)
    setSuggestions(null)
    try {
      const r = await api<{ domain: string; suggestions: MonitorSuggestion[] }>('POST', '/monitors/suggestions', { domain })
      setSuggestions(r.suggestions)
      setLooked(r.domain)
      const preselect: number[] = []
      r.suggestions.forEach((s, i) => {
        if (s.recommended && !s.already_monitored && preselect.length < room) preselect.push(i)
      })
      setPicked(preselect)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'INVALID_HOST') setFieldError(err.message)
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    } finally {
      setBusy(null)
    }
  }

  async function add() {
    if (!suggestions) return
    setBusy('adding')
    setError(null)
    const done: Outcome[] = []
    // One at a time, in the order shown, so a plan limit stops at the same place every time.
    for (const i of [...picked].sort((a, b) => a - b)) {
      const s = suggestions[i]
      try {
        const r = await api<{ monitor: { id: string } }>('POST', '/monitors', s.monitor)
        done.push({ name: s.monitor.name, id: r.monitor.id })
      } catch (err) {
        const e = err instanceof ApiError ? err : null
        done.push({ name: s.monitor.name, error: e?.message ?? 'Could not add this monitor.', upgrade: e?.code === 'PLAN_LIMIT_REACHED' })
      }
    }
    setOutcomes(done)
    setSuggestions(null)
    setBusy(null)
    api<{ usage: Usage }>('GET', '/usage').then((r) => setUsage(r.usage), () => {})
  }

  const toggle = (i: number, on: boolean) => setPicked((p) => (on ? [...p, i] : p.filter((x) => x !== i)))
  const added = outcomes?.filter((o) => o.id) ?? []
  const failed = outcomes?.filter((o) => o.error) ?? []

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.monitors} />
        <h1>Set up from your domain</h1>
      </div>
      <p>
        Enter your website’s domain and HawkEye will look at it from the outside, the way its checks will, and suggest monitors: your home page, a health
        endpoint if it has one, an API on an api subdomain, and your email’s MX records. You choose which to add; nothing is created until you do.
      </p>

      <section className="card narrow-left" aria-labelledby="domain-h">
        <h2 id="domain-h" className="sr-only">Your domain</h2>
        <form onSubmit={look}>
          <Field label="Domain" required maxLength={253} value={domain} onChange={(e) => setDomain(e.target.value)} error={fieldError} placeholder="example.com"
            hint="A full address works too; only the domain is used." />
          <button type="submit" disabled={busy !== null}>
            {busy === 'looking' ? <><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Looking at your site…</> : 'Suggest monitors'}
          </button>
        </form>
      </section>

      {error && <Alert>{error}</Alert>}

      {suggestions && (
        <section className="card" aria-labelledby="suggest-h">
          <h2 id="suggest-h">Suggested for {looked}</h2>
          <fieldset className="group suggestions">
            <legend className="sr-only">Monitors to add</legend>
            {suggestions.map((s, i) => {
              const on = picked.includes(i)
              const full = !on && picked.length >= room
              return (
                <label key={i}>
                  <input type="checkbox" checked={on} disabled={s.already_monitored || full} onChange={(e) => toggle(i, e.target.checked)} />
                  <span>
                    <strong>{KIND_LABELS[s.kind]}</strong>: {target(s)}
                    {s.already_monitored && <span className="hint"> (already monitored)</span>}
                  </span>
                  <small className="hint">{s.reason}</small>
                </label>
              )
            })}
          </fieldset>
          {usage && room < suggestions.filter((s) => !s.already_monitored).length && (
            <p className="hint" role="status">
              Your plan has room for {room} more monitor{room === 1 ? '' : 's'}. <Link to="/billing">See plans</Link>
            </p>
          )}
          <p className="hint">Each is checked at your plan’s fastest interval. You can change the interval, alerts and anything else afterward.</p>
          <button type="button" className="primary" disabled={picked.length === 0 || busy !== null} onClick={() => void add()}>
            {busy === 'adding' ? 'Adding…' : `Add ${picked.length} monitor${picked.length === 1 ? '' : 's'}`}
          </button>
        </section>
      )}

      {outcomes && (
        <section className="card" aria-labelledby="done-h" role="status">
          <h2 id="done-h">{added.length > 0 ? `Added ${added.length} monitor${added.length === 1 ? '' : 's'}` : 'No monitors were added'}</h2>
          {added.length > 0 && (
            <>
              <p>HawkEye is running their first checks now.</p>
              <ul className="plain-list">
                {added.map((o) => <li key={o.id}><span aria-hidden="true">✔</span> <Link to={`/monitors/${o.id}`}>{o.name}</Link></li>)}
              </ul>
            </>
          )}
          {failed.length > 0 && (
            <ul className="plain-list">
              {failed.map((o) => (
                <li key={o.name}>
                  <span aria-hidden="true">✖</span> {o.name}: {o.error} {o.upgrade && <Link to="/billing">See plans</Link>}
                </li>
              ))}
            </ul>
          )}
          <p><Link className="button" to="/monitors">Go to your monitors</Link></p>
        </section>
      )}

      <p className="hint">
        Scheduled jobs such as backups and reports can’t be seen from outside. <Link to="/monitors/new?type=heartbeat">Add a heartbeat</Link> for each one, and
        HawkEye alerts you when it stops running.
      </p>
    </>
  )
}
