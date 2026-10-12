import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { api, ApiError, type Monitor, type MonitorInput } from '../api'
import { Field } from '../components/Field'

function limitMessage(e: ApiError): string {
  const next = e.details.next_plan as { name: string; max_monitors: number } | undefined
  return next ? `${e.message} ${next.name} includes ${next.max_monitors} monitors.` : e.message
}

export function MonitorFormPage() {
  const { monitorID } = useParams()
  const editing = Boolean(monitorID)
  const navigate = useNavigate()
  const [loaded, setLoaded] = useState(!editing)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('https://')
  const [method, setMethod] = useState<'GET' | 'HEAD'>('GET')
  const [expected, setExpected] = useState('')
  const [intervalMin, setIntervalMin] = useState('')
  const [minAllowed, setMinAllowed] = useState(1)
  const [error, setError] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<{ usage: { plan: { minimum_check_interval_seconds: number } } }>('GET', '/usage')
      .then((r) => setMinAllowed(Math.ceil(r.usage.plan.minimum_check_interval_seconds / 60)))
      .catch(() => undefined)
    if (!monitorID) return
    api<{ monitor: Monitor }>('GET', `/monitors/${monitorID}`)
      .then(({ monitor: m }) => {
        setName(m.name)
        setUrl(m.url)
        setMethod(m.method)
        setExpected(m.expected_status_code?.toString() ?? '')
        setIntervalMin(String(Math.round(m.effective_check_interval_seconds / 60)))
        setLoaded(true)
      })
      .catch((e: ApiError) => setError(e))
  }, [monitorID])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const body: MonitorInput = {
      name,
      url,
      method,
      expected_status_code: expected === '' ? null : Number(expected),
    }
    if (intervalMin !== '') body.check_interval_seconds = Number(intervalMin) * 60
    try {
      if (editing) await api('PATCH', `/monitors/${monitorID}`, body)
      else await api('POST', '/monitors', body)
      navigate('/monitors')
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  const f = error?.fields ?? {}
  const generic = error && !Object.keys(f).length
  return (
    <section className="card narrow">
      <h1>{editing ? 'Edit monitor' : 'Add monitor'}</h1>
      {!loaded && !error && <p role="status">Loading…</p>}
      {generic && (
        <div role="alert" className="error">
          <p>{error.code === 'PLAN_LIMIT_REACHED' ? limitMessage(error) : error.message}</p>
        </div>
      )}
      {loaded && (
        <form onSubmit={onSubmit}>
          <Field label="Name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={f.name} />
          <Field label="URL" type="url" required value={url} onChange={(e) => setUrl(e.target.value)} error={f.url}
            hint="A public http(s) address. Private and internal addresses are not allowed." />
          <div className="field">
            <label htmlFor="method">Request method</label>
            <select id="method" value={method} onChange={(e) => setMethod(e.target.value as 'GET' | 'HEAD')}>
              <option value="GET">GET</option>
              <option value="HEAD">HEAD</option>
            </select>
          </div>
          <Field label="Expected status code (optional)" type="number" min={100} max={599} value={expected}
            onChange={(e) => setExpected(e.target.value)} error={f.expected_status_code}
            hint="Leave empty to treat any response below 400 as up." />
          <Field label="Check every (minutes)" type="number" min={minAllowed} max={1440} value={intervalMin}
            onChange={(e) => setIntervalMin(e.target.value)} error={f.check_interval_seconds}
            hint={`Your plan allows checks every ${minAllowed} minute(s) or slower. Leave empty for the default.`} />
          <div className="row">
            <button type="submit" disabled={busy}>{editing ? 'Save changes' : 'Add monitor'}</button>
            <Link to="/monitors">Cancel</Link>
          </div>
        </form>
      )}
    </section>
  )
}
