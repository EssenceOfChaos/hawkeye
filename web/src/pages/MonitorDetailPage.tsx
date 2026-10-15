import { useState } from 'react'
import { Link, useParams } from 'react-router'
import type { CheckResult, Incident, Monitor, RangeInfo, SeriesPoint, Uptime, Usage } from '../api'
import { Alert } from '../components/Alert'
import { CertificateCard } from '../components/Certificate'
import { RangePicker } from '../components/RangePicker'
import { ResponseChart } from '../components/ResponseChart'
import { StatusBadge } from '../components/StatusBadge'
import { AvgResponseTile, StatTile, UptimeHero } from '../components/Uptime'
import { availableRanges, formatDateTime, formatMs, RANGE_LABELS } from '../format'
import { useApi } from '../hooks'
import { formatDuration, IncidentStatus } from './IncidentsPage'

const PAGE = 25

export function MonitorDetailPage() {
  const { monitorID = '' } = useParams()
  const [range, setRange] = useState('24h')
  const [offset, setOffset] = useState(0)

  const usage = useApi<{ usage: Usage }>('/usage')
  const monitor = useApi<{ monitor: Monitor }>(`/monitors/${monitorID}`)
  const uptime = useApi<{ uptime: Uptime; range: RangeInfo }>(`/monitors/${monitorID}/uptime?range=${range}`)
  const series = useApi<{ points: SeriesPoint[]; bucket_seconds: number; range: RangeInfo }>(`/monitors/${monitorID}/response-times?range=${range}`)
  const results = useApi<{ results: CheckResult[]; total: number }>(`/monitors/${monitorID}/results?range=${range}&limit=${PAGE}&offset=${offset}`)
  const incidents = useApi<{ incidents: Incident[]; total: number }>(`/monitors/${monitorID}/incidents?limit=5`)

  if (monitor.error?.status === 404) {
    return (
      <section className="card empty">
        <h1>Monitor not found</h1>
        <p>It may have been deleted. <Link to="/monitors">Back to monitors</Link></p>
      </section>
    )
  }
  const m = monitor.data?.monitor
  const options = availableRanges(usage.data?.usage.plan.history_retention_days ?? 7)
  const loading = uptime.loading || series.loading
  const changeRange = (r: string) => {
    setRange(r)
    setOffset(0)
  }

  return (
    <>
      {!m && !monitor.error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}
      {monitor.error && !m && <Alert>{monitor.error.message}</Alert>}
      {m && (
        <>
          <p className="hint"><Link to="/monitors">← Monitors</Link></p>
          <div className="row">
            <h1>{m.name}</h1>
            <StatusBadge monitor={m} />
            <span className="spacer" />
            <Link className="button" to={`/monitors/${m.id}/edit`}>Edit</Link>
          </div>
          <p className="hint">
            {m.method} {m.url} · every {Math.round(m.effective_check_interval_seconds / 60)} min ·{' '}
            {m.last_checked_at ? `last checked ${formatDateTime(m.last_checked_at)}` : 'not checked yet'}
          </p>

          <CertificateCard monitor={m} />

          <RangePicker value={range} options={options} onChange={changeRange} />

          <div style={{ opacity: loading ? 0.55 : 1 }}>
            {uptime.data && (
              <>
                <UptimeHero uptime={uptime.data.uptime} label={RANGE_LABELS[range] ?? range} />
                <div className="stats">
                  <StatTile label="Checks completed" value={uptime.data.uptime.total_checks.toLocaleString()} />
                  <StatTile label="Failed checks" value={uptime.data.uptime.failed_checks.toLocaleString()} />
                  <AvgResponseTile uptime={uptime.data.uptime} />
                </div>
              </>
            )}
          </div>

          {series.data && (
            <ResponseChart
              title="Average response time"
              points={series.data.points}
              bucketSeconds={series.data.bucket_seconds}
              range={series.data.range}
              loading={series.loading}
            />
          )}

          <section className="card" aria-labelledby="inc-h">
            <div className="row">
              <h2 id="inc-h">Incidents</h2>
              <span className="spacer" />
              <Link to="/incidents">All incidents</Link>
            </div>
            {incidents.data?.incidents.length === 0 && <p>No incidents for this website.</p>}
            <ul className="plain-list">
              {incidents.data?.incidents.map((i) => (
                <li key={i.id}>
                  <IncidentStatus status={i.status} />{' '}
                  <span>{new Date(i.started_at).toLocaleString()}</span>
                  <span className="hint"> · {formatDuration(i.duration_seconds)}{i.status === 'open' && ' so far'}{i.summary ? ` · ${i.summary}` : ''}</span>
                </li>
              ))}
            </ul>
          </section>

          <section aria-labelledby="hist-h" style={{ opacity: results.loading ? 0.55 : 1 }}>
            <h2 id="hist-h">Check history</h2>
            {results.data?.results.length === 0 && <p>No checks in this period yet.</p>}
            {results.data && results.data.results.length > 0 && (
              <div className="table-wrap">
                <table>
                  <caption className="sr-only">Individual checks, newest first</caption>
                  <thead>
                    <tr><th>When</th><th>Result</th><th className="num">HTTP</th><th className="num">Response</th><th>Details</th></tr>
                  </thead>
                  <tbody>
                    {results.data.results.map((r) => (
                      <tr key={r.id}>
                        <td><time dateTime={r.checked_at}>{formatDateTime(r.checked_at)}</time></td>
                        <td>
                          {r.success
                            ? <span className="badge up"><span aria-hidden="true">✔</span> Success</span>
                            : <span className="badge down"><span aria-hidden="true">✖</span> Failed</span>}
                        </td>
                        <td className="num">{r.http_status_code ?? '—'}</td>
                        <td className="num">{formatMs(r.response_time_ms)}</td>
                        <td>{r.error_summary ?? ''}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {results.data && results.data.total > PAGE && (
              <nav className="row" aria-label="Check history pagination">
                <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>Newer</button>
                <span>{offset + 1}–{Math.min(offset + PAGE, results.data.total)} of {results.data.total.toLocaleString()}</span>
                <button disabled={offset + PAGE >= results.data.total} onClick={() => setOffset(offset + PAGE)}>Older</button>
              </nav>
            )}
          </section>
        </>
      )}
    </>
  )
}
