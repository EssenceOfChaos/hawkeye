import { Alert } from '../components/Alert'
import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import {
  api,
  ApiError,
  BODY_METHODS,
  HTTP_METHODS,
  WRITE_METHODS,
  WRITE_MIN_MINUTES,
  type Assertion,
  type AuthType,
  type HttpMethod,
  type JsonOperator,
  type Monitor,
  type MonitorInput,
  type RegionInfo,
  type RequestHeader,
} from '../api'
import { Field } from '../components/Field'

function limitMessage(e: ApiError): string {
  const next = e.details.next_plan as { name: string; max_monitors: number } | undefined
  return next ? `${e.message} ${next.name} includes ${next.max_monitors} monitors.` : e.message
}

const ASSERTION_TYPES: { value: Assertion['type']; label: string }[] = [
  { value: 'body_contains', label: 'Body contains' },
  { value: 'body_not_contains', label: 'Body does not contain' },
  { value: 'json_path', label: 'JSON value' },
]

const OPERATORS: { value: JsonOperator; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'not_equals', label: 'does not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'exists', label: 'exists' },
  { value: 'not_exists', label: 'does not exist' },
]

const needsValue = (a: Assertion) => a.type !== 'json_path' || (a.operator !== 'exists' && a.operator !== 'not_exists')

/** A field error shown under a row of inputs that has no single label. */
function RowError({ id, children }: { id: string; children?: string }) {
  return children ? (
    <small id={id} className="error row-error">
      {children}
    </small>
  ) : null
}

export function MonitorFormPage() {
  const { monitorID } = useParams()
  const editing = Boolean(monitorID)
  const navigate = useNavigate()
  const [loaded, setLoaded] = useState(!editing)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('https://')
  const [method, setMethod] = useState<HttpMethod>('GET')
  const [expected, setExpected] = useState('')
  const [intervalMin, setIntervalMin] = useState('')
  const [minAllowed, setMinAllowed] = useState(1)
  const [headers, setHeaders] = useState<RequestHeader[]>([])
  const [body, setBody] = useState('')
  const [authType, setAuthType] = useState<AuthType>('none')
  const [authUsername, setAuthUsername] = useState('')
  const [authHeaderName, setAuthHeaderName] = useState('')
  const [authSecret, setAuthSecret] = useState('')
  // The auth type whose secret is already stored; leaving the secret empty keeps it.
  const [storedAuthType, setStoredAuthType] = useState<AuthType | null>(null)
  const [assertions, setAssertions] = useState<Assertion[]>([])
  const [allRegions, setAllRegions] = useState<RegionInfo[]>([])
  const [maxRegions, setMaxRegions] = useState(1)
  const [chosen, setChosen] = useState<string[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<{ usage: { plan: { minimum_check_interval_seconds: number } } }>('GET', '/usage')
      .then((r) => setMinAllowed(Math.ceil(r.usage.plan.minimum_check_interval_seconds / 60)))
      .catch(() => undefined)
    api<{ regions: RegionInfo[]; max_regions: number }>('GET', '/regions')
      .then((r) => {
        setAllRegions(r.regions ?? [])
        setMaxRegions(r.max_regions ?? 1)
      })
      .catch(() => undefined)
    if (!monitorID) return
    api<{ monitor: Monitor }>('GET', `/monitors/${monitorID}`)
      .then(({ monitor: m }) => {
        setName(m.name)
        setUrl(m.url)
        setMethod(m.method)
        setExpected(m.expected_status_code?.toString() ?? '')
        setIntervalMin(String(Math.round(m.effective_check_interval_seconds / 60)))
        setHeaders(m.headers ?? [])
        setBody(m.body ?? '')
        if (m.auth) {
          setAuthType(m.auth.type)
          setAuthUsername(m.auth.username ?? '')
          setAuthHeaderName(m.auth.header_name ?? '')
          setStoredAuthType(m.auth.secret_set ? m.auth.type : null)
        }
        setAssertions(m.assertions ?? [])
        setChosen(m.regions ?? [])
        setLoaded(true)
      })
      .catch((e: ApiError) => setError(e))
  }, [monitorID])

  const bodyAllowed = BODY_METHODS.includes(method)
  // POST, PUT, PATCH and DELETE change things on the target, and the request is repeated on every check.
  const writeMethod = WRITE_METHODS.includes(method)
  const minIntervalMin = writeMethod ? Math.max(minAllowed, WRITE_MIN_MINUTES) : minAllowed
  const assertionsAllowed = method !== 'HEAD'
  const keepsSecret = storedAuthType === authType

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const payload: MonitorInput = {
      name,
      url,
      method,
      expected_status_code: expected === '' ? null : Number(expected),
      headers: headers.filter((h) => h.name.trim() !== '' || h.value !== ''),
      body: bodyAllowed && body !== '' ? body : null,
      auth: { type: authType },
      assertions: assertionsAllowed
        ? assertions.map((a) => (a.type === 'json_path' ? a : { type: a.type, value: a.value }))
        : [],
    }
    if (allRegions.length > 1) payload.regions = chosen
    if (authType === 'basic') payload.auth!.username = authUsername
    if (authType === 'header') payload.auth!.header_name = authHeaderName
    if (authType !== 'none' && authSecret !== '') payload.auth!.secret = authSecret
    if (intervalMin !== '') payload.check_interval_seconds = Number(intervalMin) * 60
    try {
      if (editing) await api('PATCH', `/monitors/${monitorID}`, payload)
      else await api('POST', '/monitors', payload)
      navigate('/monitors')
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  const setHeader = (i: number, patch: Partial<RequestHeader>) =>
    setHeaders((hs) => hs.map((h, j) => (j === i ? { ...h, ...patch } : h)))
  const setAssertion = (i: number, patch: Partial<Assertion>) =>
    setAssertions((as) => as.map((a, j) => (j === i ? { ...a, ...patch } : a)))

  const f = error?.fields ?? {}
  const generic = error && !Object.keys(f).length
  const secretLabel = authType === 'basic' ? 'Password' : authType === 'bearer' ? 'Token' : 'Header value'
  return (
    <section className="card narrow">
      <h1>{editing ? 'Edit monitor' : 'Add a website or API to monitor'}</h1>
      {!editing && <p>Give your monitor a recognizable name and enter the website or API URL you want HawkEye to check.</p>}
      {!loaded && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}
      {generic && (
        <Alert>{error.code === 'PLAN_LIMIT_REACHED' ? limitMessage(error) : error.message}</Alert>
      )}
      {loaded && (
        <form onSubmit={onSubmit}>
          <Field label="Name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={f.name} />
          <Field label="URL" type="url" required value={url} onChange={(e) => setUrl(e.target.value)} error={f.url}
            hint="A public http(s) address. Private and internal addresses are not allowed." />
          <div className="field">
            <label htmlFor="method">Request method</label>
            <select id="method" value={method} onChange={(e) => setMethod(e.target.value as HttpMethod)}
              aria-invalid={f.method ? true : undefined}>
              {HTTP_METHODS.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
            {f.method && <small className="error">{f.method}</small>}
            {writeMethod && (
              <Alert tone="info" role="note">
                <strong>This request is repeated on every check.</strong> Use {method} only on an endpoint you are authorized to monitor and that is safe to call
                again and again. It needs a verified email address, and checks run at most every {WRITE_MIN_MINUTES} minutes.
              </Alert>
            )}
          </div>

          <fieldset className="group">
            <legend>Request</legend>
            <div className="field">
              <span id="headers-label">Headers</span>
              {headers.map((h, i) => (
                <div key={i}>
                  <div className="kv-row">
                    <input aria-label={`Header ${i + 1} name`} placeholder="X-Header-Name" value={h.name}
                      aria-invalid={f[`headers[${i}].name`] ? true : undefined}
                      onChange={(e) => setHeader(i, { name: e.target.value })} />
                    <input aria-label={`Header ${i + 1} value`} placeholder="value" value={h.value}
                      aria-invalid={f[`headers[${i}].value`] ? true : undefined}
                      onChange={(e) => setHeader(i, { value: e.target.value })} />
                    <button type="button" className="linklike" aria-label={`Remove header ${i + 1}`}
                      onClick={() => setHeaders((hs) => hs.filter((_, j) => j !== i))}>Remove</button>
                  </div>
                  <RowError id={`h${i}-err`}>{f[`headers[${i}].name`] ?? f[`headers[${i}].value`]}</RowError>
                </div>
              ))}
              <RowError id="headers-err">{f.headers}</RowError>
              <div>
                <button type="button" className="linklike" onClick={() => setHeaders((hs) => [...hs, { name: '', value: '' }])}>
                  Add header
                </button>
              </div>
              <small className="hint">Header values are visible to everyone in your organization. Put credentials under Authentication.</small>
            </div>

            {bodyAllowed && (
              <div className="field">
                <label htmlFor="body">Request body (optional)</label>
                <textarea id="body" rows={4} value={body} onChange={(e) => setBody(e.target.value)} placeholder='{"ping": true}'
                  aria-invalid={f.body ? true : undefined} aria-describedby="body-hint" />
                <small id="body-hint" className="hint">Sent as application/json when it is valid JSON, otherwise as text. Set a Content-Type header to override.</small>
                {f.body && <small className="error">{f.body}</small>}
              </div>
            )}

            <div className="field">
              <label htmlFor="auth-type">Authentication</label>
              <select id="auth-type" value={authType} onChange={(e) => setAuthType(e.target.value as AuthType)}>
                <option value="none">None</option>
                <option value="basic">Basic (username and password)</option>
                <option value="bearer">Bearer token</option>
                <option value="header">API key header</option>
              </select>
              {f['auth.type'] && <small className="error">{f['auth.type']}</small>}
            </div>
            {authType === 'basic' && (
              <Field label="Username" value={authUsername} onChange={(e) => setAuthUsername(e.target.value)} error={f['auth.username']} />
            )}
            {authType === 'header' && (
              <Field label="Header name" placeholder="X-API-Key" value={authHeaderName}
                onChange={(e) => setAuthHeaderName(e.target.value)} error={f['auth.header_name']} />
            )}
            {authType !== 'none' && (
              <Field label={secretLabel} type="password" autoComplete="new-password" value={authSecret}
                onChange={(e) => setAuthSecret(e.target.value)} error={f['auth.secret']}
                placeholder={keepsSecret ? '••••••••' : undefined}
                hint={keepsSecret ? 'Saved and hidden. Leave empty to keep it, or enter a new one to replace it.' : 'Stored encrypted and never shown again.'} />
            )}
          </fieldset>

          <fieldset className="group">
            <legend>Response checks</legend>
            <Field label="Expected status code (optional)" type="number" min={100} max={599} value={expected}
              onChange={(e) => setExpected(e.target.value)} error={f.expected_status_code}
              hint="Leave empty to treat any response below 400 as up." />
            <div className="field">
              <span>Assertions</span>
              {!assertionsAllowed && <small className="hint">HEAD responses have no body. Use GET to check the content.</small>}
              {assertionsAllowed &&
                assertions.map((a, i) => (
                  <div key={i}>
                    <div className="kv-row">
                      <select aria-label={`Assertion ${i + 1} type`} value={a.type}
                        onChange={(e) => setAssertion(i, { type: e.target.value as Assertion['type'], operator: a.operator ?? 'equals' })}>
                        {ASSERTION_TYPES.map((t) => (
                          <option key={t.value} value={t.value}>{t.label}</option>
                        ))}
                      </select>
                      <button type="button" className="linklike" aria-label={`Remove assertion ${i + 1}`}
                        onClick={() => setAssertions((as) => as.filter((_, j) => j !== i))}>Remove</button>
                    </div>
                    {a.type === 'json_path' && (
                      <div className="kv-row">
                        <input aria-label={`Assertion ${i + 1} JSON path`} placeholder="data.status" value={a.path ?? ''}
                          aria-invalid={f[`assertions[${i}].path`] ? true : undefined}
                          onChange={(e) => setAssertion(i, { path: e.target.value })} />
                        <select aria-label={`Assertion ${i + 1} operator`} value={a.operator ?? 'equals'}
                          onChange={(e) => setAssertion(i, { operator: e.target.value as JsonOperator })}>
                          {OPERATORS.map((o) => (
                            <option key={o.value} value={o.value}>{o.label}</option>
                          ))}
                        </select>
                      </div>
                    )}
                    {needsValue(a) && (
                      <div className="kv-row">
                        <input aria-label={`Assertion ${i + 1} value`} placeholder={a.type === 'json_path' ? 'ok' : 'text to look for'}
                          value={a.value ?? ''} aria-invalid={f[`assertions[${i}].value`] ? true : undefined}
                          onChange={(e) => setAssertion(i, { value: e.target.value })} />
                      </div>
                    )}
                    <RowError id={`a${i}-err`}>
                      {f[`assertions[${i}].type`] ?? f[`assertions[${i}].path`] ?? f[`assertions[${i}].operator`] ?? f[`assertions[${i}].value`]}
                    </RowError>
                  </div>
                ))}
              <RowError id="assertions-err">{f.assertions}</RowError>
              {assertionsAllowed && (
                <div>
                  <button type="button" className="linklike"
                    onClick={() => setAssertions((as) => [...as, { type: 'body_contains', value: '' }])}>
                    Add assertion
                  </button>
                </div>
              )}
              <small className="hint">
                All assertions must pass for the check to count as up. JSON paths look like data.items[0].status.
              </small>
            </div>
          </fieldset>

          {allRegions.length > 1 && (
            <fieldset className="group">
              <legend>Check from</legend>
              {allRegions.map((r, i) => {
                const on = chosen.includes(r.code) || (chosen.length === 0 && i === 0)
                return (
                  <label key={r.code}>
                    <input type="checkbox" checked={on} disabled={!on && chosen.length >= maxRegions}
                      onChange={(e) => {
                        // Leaving everything unticked would mean "primary", so the list always starts from what is shown.
                        const current = chosen.length === 0 ? [allRegions[0].code] : chosen
                        setChosen(e.target.checked ? [...current, r.code] : current.filter((c) => c !== r.code))
                      }} /> {r.name}
                  </label>
                )
              })}
              {f.regions && <small className="error">{f.regions}</small>}
              <small className="hint">
                {maxRegions > 1
                  ? `Your plan can check each monitor from up to ${maxRegions} locations. A monitor counts as down only when most of its locations agree, so a problem at one location never raises an alert.`
                  : 'Checking from several locations is available on paid plans. A monitor counts as down only when most of its locations agree.'}
              </small>
            </fieldset>
          )}

          <Field label="Check every (minutes)" type="number" min={minIntervalMin} max={1440} value={intervalMin}
            onChange={(e) => setIntervalMin(e.target.value)} error={f.check_interval_seconds}
            hint={writeMethod
              ? `${method} monitors run at most every ${WRITE_MIN_MINUTES} minutes. Leave empty for the default.`
              : `Your plan allows checks every ${minAllowed} minute(s) or slower. Leave empty for the default.`} />
          <div className="row">
            <button type="submit" disabled={busy}>{editing ? 'Save changes' : 'Add monitor'}</button>
            <Link to="/monitors">Cancel</Link>
          </div>
        </form>
      )}
    </section>
  )
}
