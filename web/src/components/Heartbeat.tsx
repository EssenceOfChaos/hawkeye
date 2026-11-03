import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, pingURL, type HeartbeatPing, type Monitor } from '../api'
import { formatDateTime } from '../format'
import { Alert } from './Alert'
import { CopyField } from './BadgeCard'

const UNITS = [
  { value: 86400, label: 'days' },
  { value: 3600, label: 'hours' },
  { value: 60, label: 'minutes' },
] as const

/** The largest unit that divides a duration evenly, so 86400 is shown as 1 day rather than 1440 minutes. */
function unitFor(seconds: number): number {
  return UNITS.find((u) => seconds % u.value === 0)?.value ?? 60
}

/** A duration in words: "5 minutes", "1 hour", "2 days". */
export function formatPeriod(seconds: number): string {
  const unit = unitFor(seconds)
  const n = Math.round(seconds / unit)
  const word = unit === 86400 ? 'day' : unit === 3600 ? 'hour' : 'minute'
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

/** A number and a unit (minutes, hours or days), reported to the parent in seconds. */
export function DurationField({ id, label, seconds, onChange, error, hint }: {
  id: string
  label: string
  seconds: number
  onChange: (seconds: number) => void
  error?: string
  hint?: string
}) {
  const [unit, setUnit] = useState(() => unitFor(seconds))
  const [text, setText] = useState(() => String(seconds / unitFor(seconds)))
  const report = (t: string, u: number) => {
    const n = Number(t)
    if (t !== '' && Number.isFinite(n) && n > 0) onChange(Math.round(n * u))
  }
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <div className="kv-row">
        <input id={id} type="number" min={1} step="any" required value={text} aria-invalid={error ? true : undefined}
          onChange={(e) => { setText(e.target.value); report(e.target.value, unit) }} />
        <select aria-label={`${label}: unit`} value={unit} onChange={(e) => { const u = Number(e.target.value); setUnit(u); report(text, u) }}>
          {UNITS.map((u) => <option key={u.value} value={u.value}>{u.label}</option>)}
        </select>
      </div>
      {error && <small className="error">{error}</small>}
      {hint && <small className="hint">{hint}</small>}
    </div>
  )
}

/** Ready-to-paste ways of pinging, for the common kinds of scheduled job. */
export function pingExamples(url: string): { label: string; code: string }[] {
  return [
    { label: 'Crontab line', code: `0 2 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 5 -o /dev/null ${url}` },
    { label: 'Report failures too (exit status)', code: `0 2 * * * /usr/local/bin/backup.sh; curl -fsS -m 10 --retry 5 -o /dev/null ${url}/$?` },
    { label: 'End of a shell script', code: `curl -fsS -m 10 --retry 5 -o /dev/null ${url}` },
    { label: 'Windows PowerShell', code: `Invoke-RestMethod -Uri "${url}" -TimeoutSec 10` },
  ]
}

/**
 * Where a heartbeat monitor's job sends its pings, with examples to copy. Anyone who can edit monitors can give it a new address (if the old one leaked).
 */
export function PingSetup({ monitor, onChanged }: { monitor: Monitor; onChanged: (m: Monitor) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const hb = monitor.heartbeat
  if (!hb) return null
  const url = pingURL(hb.ping_token)

  async function rotate() {
    if (!window.confirm('Give this monitor a new ping address? The current address stops working at once, so update your job to use the new one.')) return
    setBusy(true)
    setError(null)
    try {
      onChanged((await api<{ monitor: Monitor }>('POST', `/monitors/${monitor.id}/ping-token`)).monitor)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="card" aria-labelledby="ping-h">
      <h2 id="ping-h">Ping address</h2>
      {!hb.last_ping_at && (
        <Alert tone="info" role="note">
          <strong>Waiting for the first ping.</strong> Add the request below to your job. Until the first ping arrives, HawkEye does not alert, so you can set
          things up at your own pace.
        </Alert>
      )}
      <p className="hint">
        Have your job request this address each time it finishes. Keep it private: anyone who has it can report that your job ran.
      </p>
      {error && <Alert>{error}</Alert>}
      <CopyField label="Ping address" value={url} mono />
      {pingExamples(url).map((x) => <CopyField key={x.label} label={x.label} value={x.code} mono />)}
      <details className="explain">
        <summary>How pings work</summary>
        <ul>
          <li><code>{'/ping/<token>'}</code> means the job ran successfully. GET, HEAD and POST all work.</li>
          <li><code>{'/ping/<token>/fail'}</code> reports a failure, and <code>{'/ping/<token>/<exit status>'}</code> reports the job’s exit status: 0 is success, anything else a failure. A failure alerts at once.</li>
          <li>Send the end of the job’s output as the body of a POST (up to 1,000 characters are kept) and it appears in the ping log below.</li>
          <li><code>--retry 5</code> makes curl try again if the network hiccups, so one lost request does not look like a missed run.</li>
        </ul>
        <p><Link to="/docs/cron-job-monitoring">Read the full guide</Link></p>
      </details>
      <div className="row">
        <button type="button" className="linklike" disabled={busy} onClick={() => void rotate()}>Get a new ping address</button>
      </div>
    </section>
  )
}

/** A heartbeat monitor's most recent pings. */
export function PingLog({ monitorID, refresh }: { monitorID: string; refresh: number }) {
  const [pings, setPings] = useState<HeartbeatPing[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    api<{ pings: HeartbeatPing[] }>('GET', `/monitors/${monitorID}/pings?limit=50`)
      .then((r) => { setPings(r.pings); setError(null) })
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the ping log.'))
  }, [monitorID, refresh])
  return (
    <section className="card" aria-labelledby="pings-h">
      <h2 id="pings-h">Recent pings</h2>
      {error && <Alert>{error}</Alert>}
      {pings?.length === 0 && <p>No pings yet.</p>}
      {pings && pings.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">The latest pings from this job, newest first</caption>
            <thead>
              <tr><th>When</th><th>Result</th><th>From</th><th>Output</th></tr>
            </thead>
            <tbody>
              {pings.map((p) => (
                <tr key={p.id}>
                  <td><time dateTime={p.received_at}>{formatDateTime(p.received_at)}</time></td>
                  <td>
                    {p.kind === 'success'
                      ? <span className="badge up"><span aria-hidden="true">✔</span> Success</span>
                      : <span className="badge down"><span aria-hidden="true">✖</span> Failure{p.exit_status != null && ` (exit ${p.exit_status})`}</span>}
                    {p.ignored && <div className="hint">Paused: not counted</div>}
                  </td>
                  <td>
                    <span className="mono">{p.source_ip}</span>
                    <div className="hint">{p.method}{p.user_agent ? ` · ${p.user_agent}` : ''}</div>
                  </td>
                  <td>{p.body ? <pre className="ping-body">{p.body}</pre> : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
