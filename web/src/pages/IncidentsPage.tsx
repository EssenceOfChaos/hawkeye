import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type Incident } from '../api'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'

const PAGE_SIZE = 25

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

const CAUSES: Record<string, string> = {
  dns: 'DNS lookup failed',
  connect: 'Could not connect',
  tls: 'TLS / certificate problem',
  timeout: 'Timed out',
  redirect: 'Too many redirects',
  blocked: 'Address not permitted',
  http_status: 'Unexpected HTTP status',
  assertion: 'Response check failed',
  other: 'Request failed',
}

export function IncidentStatus({ status }: { status: Incident['status'] }) {
  return status === 'open' ? (
    <span className="badge down"><span aria-hidden="true">✖</span> Ongoing</span>
  ) : (
    <span className="badge up"><span aria-hidden="true">✔</span> Resolved</span>
  )
}

export function IncidentsPage() {
  const [items, setItems] = useState<Incident[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [filter, setFilter] = useState<'' | 'open' | 'resolved'>('')
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const q = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) })
      if (filter) q.set('status', filter)
      const r = await api<{ incidents: Incident[]; total: number }>('GET', `/incidents?${q}`)
      setItems(r.incidents)
      setTotal(r.total)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load incidents.')
    }
  }, [offset, filter])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <>
      <div className="row">
        <PageIcon src={brand.alertBell} />
        <h1>Incidents</h1>
        <span className="spacer" />
        <label className="inline-label">
          Show{' '}
          <select value={filter} onChange={(e) => { setOffset(0); setFilter(e.target.value as typeof filter) }}>
            <option value="">All</option>
            <option value="open">Ongoing</option>
            <option value="resolved">Resolved</option>
          </select>
        </label>
      </div>
      <p>An incident starts when an outage is confirmed by consecutive failed checks and ends when the site has recovered.</p>

      {error && <p role="alert" className="error">{error}</p>}
      {items === null && !error && <p role="status">Loading incidents…</p>}
      {items?.length === 0 && (
        <section className="card empty">
          <h2>{filter ? 'No incidents match' : 'No incidents to report'}</h2>
          <p>When a confirmed outage occurs you’ll see its timeline here, and HawkEye will email your alert recipients.</p>
        </section>
      )}
      {items && items.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Incidents, newest first</caption>
            <thead>
              <tr><th>Website</th><th>Status</th><th>Started</th><th>Duration</th><th>Cause</th></tr>
            </thead>
            <tbody>
              {items.map((i) => (
                <tr key={i.id}>
                  <td><Link to={`/monitors/${i.monitor_id}/edit`}>{i.monitor_name}</Link></td>
                  <td><IncidentStatus status={i.status} /></td>
                  <td><time dateTime={i.started_at}>{new Date(i.started_at).toLocaleString()}</time></td>
                  <td>{formatDuration(i.duration_seconds)}{i.status === 'open' && ' so far'}</td>
                  <td>{i.summary ?? (i.cause_category ? CAUSES[i.cause_category] ?? i.cause_category : '—')}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {total > PAGE_SIZE && (
        <nav className="row" aria-label="Pagination">
          <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>Previous</button>
          <span>{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
          <button disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset(offset + PAGE_SIZE)}>Next</button>
        </nav>
      )}
    </>
  )
}
