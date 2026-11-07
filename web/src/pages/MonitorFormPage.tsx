import { Alert } from '../components/Alert'
import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import {
  api,
  ApiError,
  BODY_METHODS,
  DNS_RECORD_TYPES,
  HTTP_METHODS,
  WRITE_METHODS,
  WRITE_MIN_MINUTES,
  type Assertion,
  type AuthType,
  type DnsInput,
  type DnsRecordType,
  type HeartbeatInput,
  type HttpMethod,
  type JsonOperator,
  type Monitor,
  type MonitorInput,
  type MonitorType,
  type RegionInfo,
  type RequestHeader,
  type QuietHours,
  type TagSummary,
} from '../api'
import { Field } from '../components/Field'
import { KeywordChecks } from '../components/KeywordChecks'
import { TagPicker } from '../components/Tags'
import { AlertTiming } from '../components/AlertTiming'
import { DurationField } from '../components/Heartbeat'
import { Toggle } from '../components/Toggle'

/** The server allows this many checks in total: text checks and JSON checks together. */
const MAX_CHECKS = 10

/** Where each submitted assertion came from, so a field error (assertions[i].…) can be shown on the right row. */
type Origin = { list: 'contains' | 'notContains' | 'json'; row: number }

function limitMessage(e: ApiError): string {
  const next = e.details.next_plan as { name: string; max_monitors: number } | undefined
  return next ? `${e.message} ${next.name} includes ${next.max_monitors} monitors.` : e.message
}

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

/** What the expected value of each DNS record type looks like, and how it is compared with the answers. */
const DNS_EXPECTED: Record<DnsRecordType, { label: string; placeholder: string; hint: string }> = {
  A: { label: 'Expected IPv4 address (optional)', placeholder: '203.0.113.10', hint: 'One of the addresses must match exactly. Leave empty to accept any address.' },
  AAAA: { label: 'Expected IPv6 address (optional)', placeholder: '2001:db8::1', hint: 'One of the addresses must match exactly. Leave empty to accept any address.' },
  MX: { label: 'Expected mail server (optional)', placeholder: 'mail.example.com', hint: 'One of the mail servers must match exactly. Leave empty to accept any.' },
  NS: { label: 'Expected name server (optional)', placeholder: 'ns1.example.com', hint: 'One of the name servers must match exactly. Leave empty to accept any.' },
  TXT: { label: 'Expected text (optional)', placeholder: 'v=spf1 include:_spf.example.com', hint: 'One of the TXT records must contain this text, in the same upper and lower case. Leave empty to accept any.' },
}

/** The threshold offered when slow-response alerts are turned on. */
const DEFAULT_SLOW_MS = 2000

export function MonitorFormPage() {
  const { monitorID } = useParams()
  const editing = Boolean(monitorID)
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [kind, setKind] = useState<MonitorType>(params.get('type') === 'heartbeat' ? 'heartbeat' : params.get('type') === 'dns' ? 'dns' : 'http')
  const [period, setPeriod] = useState(86400) // heartbeats: expected time between pings, in seconds
  const [grace, setGrace] = useState(3600)
  const [host, setHost] = useState('') // DNS monitors: the host name to look up
  const [recordType, setRecordType] = useState<DnsRecordType>('A')
  const [dnsExpected, setDnsExpected] = useState('')
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
  const [assertions, setAssertions] = useState<Assertion[]>([]) // JSON checks; text checks live in the two lists below
  const [contains, setContains] = useState<string[]>([])
  const [notContains, setNotContains] = useState<string[]>([])
  const [sent, setSent] = useState<Origin[]>([])
  const [allRegions, setAllRegions] = useState<RegionInfo[]>([])
  const [maxRegions, setMaxRegions] = useState(1)
  const [chosen, setChosen] = useState<string[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  const [slowOn, setSlowOn] = useState(false)
  const [slowMs, setSlowMs] = useState(String(DEFAULT_SLOW_MS))
  const [slowChecks, setSlowChecks] = useState('3')
  const [avgMs, setAvgMs] = useState<number | null>(null) // this monitor's average response over the last 24 hours, as a guide
  const [allTags, setAllTags] = useState<TagSummary[]>([])
  const [tagIDs, setTagIDs] = useState<string[]>([])
  const [alertDelay, setAlertDelay] = useState(0)
  const [quiet, setQuiet] = useState<QuietHours | null>(null)

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
    api<{ tags: TagSummary[] }>('GET', '/tags')
      .then((r) => setAllTags(r.tags ?? []))
      .catch(() => undefined)
    if (!monitorID) return
    api<{ monitor: Monitor }>('GET', `/monitors/${monitorID}`)
      .then(({ monitor: m }) => {
        setKind(m.type ?? 'http')
        if (m.heartbeat) {
          setPeriod(m.check_interval_seconds)
          setGrace(m.heartbeat.grace_seconds)
        }
        if (m.dns) {
          setHost(m.url)
          setRecordType(m.dns.record_type)
          setDnsExpected(m.dns.expected ?? '')
        }
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
        const all = m.assertions ?? []
        setContains(all.filter((a) => a.type === 'body_contains').map((a) => a.value ?? ''))
        setNotContains(all.filter((a) => a.type === 'body_not_contains').map((a) => a.value ?? ''))
        setAssertions(all.filter((a) => a.type === 'json_path'))
        setChosen(m.regions ?? [])
        setSlowOn(m.slow_threshold_ms != null)
        if (m.slow_threshold_ms != null) setSlowMs(String(m.slow_threshold_ms))
        setSlowChecks(String(m.slow_checks ?? 3))
        setTagIDs((m.tags ?? []).map((t) => t.id))
        setAlertDelay(m.alert_delay_minutes ?? 0)
        setQuiet(m.quiet_hours ?? null)
        setLoaded(true)
      })
      .catch((e: ApiError) => setError(e))
    api<{ uptime: { avg_response_ms: number | null } }>('GET', `/monitors/${monitorID}/uptime?range=24h`)
      .then((r) => setAvgMs(r.uptime.avg_response_ms != null ? Math.round(r.uptime.avg_response_ms) : null))
      .catch(() => undefined)
  }, [monitorID])

  const bodyAllowed = BODY_METHODS.includes(method)
  // POST, PUT, PATCH and DELETE change things on the target, and the request is repeated on every check.
  const writeMethod = WRITE_METHODS.includes(method)
  const minIntervalMin = writeMethod ? Math.max(minAllowed, WRITE_MIN_MINUTES) : minAllowed
  const assertionsAllowed = method !== 'HEAD'
  const keepsSecret = storedAuthType === authType

  async function onSubmitHeartbeat() {
    const payload: HeartbeatInput = { name, check_interval_seconds: period, grace_seconds: grace, tag_ids: tagIDs, alert_delay_minutes: alertDelay, quiet_hours: quiet }
    try {
      if (editing) {
        await api('PATCH', `/monitors/${monitorID}`, payload)
        navigate(`/monitors/${monitorID}`)
      } else {
        // The new monitor's page shows its ping address, which the job needs next.
        const r = await api<{ monitor: Monitor }>('POST', '/monitors', { ...payload, type: 'heartbeat' })
        navigate(`/monitors/${r.monitor.id}`)
      }
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  async function onSubmitDns() {
    const payload: DnsInput = {
      name, url: host, dns_record_type: recordType, dns_expected: dnsExpected.trim() === '' ? null : dnsExpected,
      tag_ids: tagIDs, alert_delay_minutes: alertDelay, quiet_hours: quiet,
    }
    if (allRegions.length > 1) payload.regions = chosen
    if (intervalMin !== '') payload.check_interval_seconds = Number(intervalMin) * 60
    try {
      if (editing) await api('PATCH', `/monitors/${monitorID}`, payload)
      else await api('POST', '/monitors', { ...payload, type: 'dns' })
      navigate('/monitors')
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    if (kind === 'heartbeat') return onSubmitHeartbeat()
    if (kind === 'dns') return onSubmitDns()
    // Text checks first, then JSON checks. Empty text rows are dropped rather than refused.
    const checks: { assertion: Assertion; origin: Origin }[] = [
      ...contains.map((v, row) => ({ v, row })).filter((x) => x.v.trim() !== '')
        .map(({ v, row }) => ({ assertion: { type: 'body_contains' as const, value: v }, origin: { list: 'contains' as const, row } })),
      ...notContains.map((v, row) => ({ v, row })).filter((x) => x.v.trim() !== '')
        .map(({ v, row }) => ({ assertion: { type: 'body_not_contains' as const, value: v }, origin: { list: 'notContains' as const, row } })),
      ...assertions.map((a, row) => ({ assertion: a, origin: { list: 'json' as const, row } })),
    ]
    setSent(checks.map((c) => c.origin))
    const payload: MonitorInput = {
      name,
      url,
      method,
      expected_status_code: expected === '' ? null : Number(expected),
      headers: headers.filter((h) => h.name.trim() !== '' || h.value !== ''),
      body: bodyAllowed && body !== '' ? body : null,
      auth: { type: authType },
      assertions: assertionsAllowed ? checks.map((c) => c.assertion) : [],
      slow_threshold_ms: slowOn ? Number(slowMs) : null,
      slow_checks: Number(slowChecks),
      tag_ids: tagIDs,
      alert_delay_minutes: alertDelay,
      quiet_hours: quiet,
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
  const totalChecks = contains.length + notContains.length + assertions.length
  // A field error for one submitted check, found through where that check came from.
  const errorAt = (list: Origin['list'], row: number): string | undefined => {
    const i = sent.findIndex((o) => o.list === list && o.row === row)
    if (i < 0) return undefined
    return f[`assertions[${i}].type`] ?? f[`assertions[${i}].path`] ?? f[`assertions[${i}].operator`] ?? f[`assertions[${i}].value`]
  }
  const rowErrors = (list: Origin['list'], n: number) => Object.fromEntries(Array.from({ length: n }, (_, row) => [row, errorAt(list, row)]))
  const generic = error && !Object.keys(f).length
  const secretLabel = authType === 'basic' ? 'Password' : authType === 'bearer' ? 'Token' : 'Header value'
  const regionPicker = allRegions.length > 1 && (
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
          ? `Your plan can check each monitor from up to ${maxRegions} locations. A monitor counts as offline only when most of its locations agree, so a problem at one location never raises an alert.`
          : 'Checking from several locations is available on paid plans. A monitor counts as offline only when most of its locations agree.'}
      </small>
    </fieldset>
  )
  return (
    <section className="card narrow">
      <h1>{editing ? 'Edit monitor' : kind === 'heartbeat' ? 'Monitor a cron job or scheduled task' : kind === 'dns' ? 'Monitor a DNS record' : 'Add a website or API to monitor'}</h1>
      {!editing && (
        <fieldset className="field monitor-kind">
          <legend>What do you want to monitor?</legend>
          <label>
            <input type="radio" name="kind" checked={kind === 'http'} onChange={() => setKind('http')} /> A website or API
            <small className="hint"> HawkEye checks it on a schedule.</small>
          </label>
          <label>
            <input type="radio" name="kind" checked={kind === 'heartbeat'} onChange={() => setKind('heartbeat')} /> A cron job or scheduled task (heartbeat)
            <small className="hint"> Your job pings HawkEye each time it runs; you are alerted when a ping is late or reports a failure.</small>
          </label>
          <label>
            <input type="radio" name="kind" checked={kind === 'dns'} onChange={() => setKind('dns')} /> A DNS record
            <small className="hint"> HawkEye looks it up on a schedule; you are alerted when it disappears or stops matching the value you expect.</small>
          </label>
        </fieldset>
      )}
      {!editing && kind === 'http' && <p>Give your monitor a recognizable name and enter the website or API URL you want HawkEye to check.</p>}
      {!loaded && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}
      {generic && (
        <Alert>{error.code === 'PLAN_LIMIT_REACHED' ? limitMessage(error) : error.message}</Alert>
      )}
      {loaded && kind === 'heartbeat' && (
        <form onSubmit={onSubmit}>
          <Field label="Name" required maxLength={100} value={name} placeholder="Nightly database backup" onChange={(e) => setName(e.target.value)} error={f.name} />
          <TagPicker tags={allTags} selected={tagIDs} onChange={setTagIDs} error={f.tag_ids}
            onCreated={(t) => setAllTags((ts) => [...ts, t].sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())))} />
          <DurationField id="period" label="Expect a ping every" seconds={period} onChange={setPeriod} error={f.check_interval_seconds}
            hint={`How often the job runs: 1 day for a nightly job, 1 hour for an hourly one. Your plan allows ${minAllowed} minute(s) or more, up to 30 days.`} />
          <DurationField id="grace" label="Grace time" seconds={grace} onChange={setGrace} error={f.grace_seconds}
            hint="How late a ping may be before it counts as missed: allow for the job's usual run time and a little more (1 minute to 7 days)." />
          <AlertTiming delay={alertDelay} onDelay={setAlertDelay} quiet={quiet} onQuiet={setQuiet} errors={f} />
          <div className="row">
            <button type="submit" disabled={busy}>{editing ? 'Save changes' : 'Create and show the ping address'}</button>
            <Link to={editing ? `/monitors/${monitorID}` : '/monitors'}>Cancel</Link>
          </div>
        </form>
      )}
      {loaded && kind === 'dns' && (
        <form onSubmit={onSubmit}>
          <Field label="Name" required maxLength={100} value={name} placeholder="Mail records for example.com" onChange={(e) => setName(e.target.value)} error={f.name} />
          <Field label="Host name" required value={host} placeholder="example.com" onChange={(e) => setHost(e.target.value)} error={f.url}
            hint="A public host name, such as example.com or _dmarc.example.com. Addresses and names on private networks are not allowed." />
          <TagPicker tags={allTags} selected={tagIDs} onChange={setTagIDs} error={f.tag_ids}
            onCreated={(t) => setAllTags((ts) => [...ts, t].sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())))} />
          <div className="field">
            <label htmlFor="record-type">Record type</label>
            <select id="record-type" value={recordType} aria-invalid={f.dns_record_type ? true : undefined}
              onChange={(e) => {
                setRecordType(e.target.value as DnsRecordType)
                setDnsExpected('') // a value written for one record type means nothing for another
              }}>
              {DNS_RECORD_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
            </select>
            {f.dns_record_type && <small className="error">{f.dns_record_type}</small>}
          </div>
          <Field label={DNS_EXPECTED[recordType].label} value={dnsExpected} maxLength={255} placeholder={DNS_EXPECTED[recordType].placeholder}
            onChange={(e) => setDnsExpected(e.target.value)} error={f.dns_expected} hint={DNS_EXPECTED[recordType].hint} />
          <AlertTiming delay={alertDelay} onDelay={setAlertDelay} quiet={quiet} onQuiet={setQuiet} errors={f} />
          {regionPicker}
          <Field label="Check every (minutes)" type="number" min={minAllowed} max={1440} value={intervalMin}
            onChange={(e) => setIntervalMin(e.target.value)} error={f.check_interval_seconds}
            hint={`Your plan allows checks every ${minAllowed} minute(s) or slower. Leave empty for the default. A changed record shows up here once the old one expires from DNS caches.`} />
          <div className="row">
            <button type="submit" disabled={busy}>{editing ? 'Save changes' : 'Add monitor'}</button>
            <Link to="/monitors">Cancel</Link>
          </div>
        </form>
      )}
      {loaded && kind === 'http' && (
        <form onSubmit={onSubmit}>
          <Field label="Name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={f.name} />
          <Field label="URL" type="url" required value={url} onChange={(e) => setUrl(e.target.value)} error={f.url}
            hint="A public http(s) address. Private and internal addresses are not allowed." />
          <TagPicker tags={allTags} selected={tagIDs} onChange={setTagIDs} error={f.tag_ids}
            onCreated={(t) => setAllTags((ts) => [...ts, t].sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())))} />
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

          <KeywordChecks contains={contains} notContains={notContains} onContains={setContains} onNotContains={setNotContains}
            disabled={!assertionsAllowed} full={totalChecks >= MAX_CHECKS}
            errors={{ contains: rowErrors('contains', contains.length), notContains: rowErrors('notContains', notContains.length) }} />

          <fieldset className="group">
            <legend>Response checks</legend>
            <Field label="Expected status code (optional)" type="number" min={100} max={599} value={expected}
              onChange={(e) => setExpected(e.target.value)} error={f.expected_status_code}
              hint="Leave empty to treat any response below 400 as online." />
            <div className="field">
              <span>JSON checks (for APIs)</span>
              {!assertionsAllowed && <small className="hint">HEAD responses have no body. Use GET to check the content.</small>}
              {assertionsAllowed &&
                assertions.map((a, i) => (
                  <div key={i}>
                    <div className="kv-row">
                      <input aria-label={`JSON check ${i + 1} path`} placeholder="data.status" value={a.path ?? ''}
                        aria-invalid={errorAt('json', i) ? true : undefined}
                        onChange={(e) => setAssertion(i, { path: e.target.value })} />
                      <select aria-label={`JSON check ${i + 1} operator`} value={a.operator ?? 'equals'}
                        onChange={(e) => setAssertion(i, { operator: e.target.value as JsonOperator })}>
                        {OPERATORS.map((o) => (
                          <option key={o.value} value={o.value}>{o.label}</option>
                        ))}
                      </select>
                      <button type="button" className="linklike" aria-label={`Remove JSON check ${i + 1}`}
                        onClick={() => setAssertions((as) => as.filter((_, j) => j !== i))}>Remove</button>
                    </div>
                    {needsValue(a) && (
                      <div className="kv-row">
                        <input aria-label={`JSON check ${i + 1} value`} placeholder="ok"
                          value={a.value ?? ''} aria-invalid={errorAt('json', i) ? true : undefined}
                          onChange={(e) => setAssertion(i, { value: e.target.value })} />
                      </div>
                    )}
                    <RowError id={`a${i}-err`}>{errorAt('json', i)}</RowError>
                  </div>
                ))}
              <RowError id="assertions-err">{f.assertions}</RowError>
              {assertionsAllowed && (
                <div>
                  <button type="button" className="linklike" disabled={totalChecks >= MAX_CHECKS}
                    onClick={() => setAssertions((as) => [...as, { type: 'json_path', path: '', operator: 'equals', value: '' }])}>
                    Add JSON check
                  </button>
                </div>
              )}
              <small className="hint">
                For an API that answers in JSON: check a value at a path such as data.items[0].status. Every check (text and JSON) must pass for the monitor to
                count as online; up to {MAX_CHECKS} in total.
              </small>
            </div>
          </fieldset>

          <fieldset className="group">
            <legend>Slow-response alert</legend>
            <Toggle checked={slowOn} onToggle={setSlowOn}>Alert me when this site responds slowly</Toggle>
            {slowOn ? (
              <>
                <Field label="Slower than (milliseconds)" type="number" required min={100} max={59999} step={100} value={slowMs}
                  onChange={(e) => setSlowMs(e.target.value)} error={f.slow_threshold_ms}
                  hint={`${avgMs != null ? `Over the last 24 hours it averaged ${avgMs.toLocaleString()} ms. ` : 'Most websites answer well within 1,000 ms. '}A threshold of two to three times the usual time avoids alerts for normal ups and downs.`} />
                <div className="field">
                  <label htmlFor="slow-checks">For this many checks in a row</label>
                  <select id="slow-checks" value={slowChecks} onChange={(e) => setSlowChecks(e.target.value)} aria-invalid={f.slow_checks ? true : undefined}>
                    {[2, 3, 4, 5, 6, 8, 10].map((n) => <option key={n} value={n}>{n}</option>)}
                  </select>
                  {f.slow_checks && <small className="error">{f.slow_checks}</small>}
                  <small className="hint">
                    One slow response never alerts. You get one alert when slowness is confirmed and one when responses are back to normal for as many checks.
                    A slow site still counts as online.
                  </small>
                </div>
              </>
            ) : (
              f.slow_threshold_ms && <small className="error">{f.slow_threshold_ms}</small>
            )}
          </fieldset>

          <AlertTiming delay={alertDelay} onDelay={setAlertDelay} quiet={quiet} onQuiet={setQuiet} errors={f} />

          {regionPicker}

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
