import { useState } from 'react'
import { Link, useParams } from 'react-router'
import type { CheckResult, Incident, Monitor, Plan, RangeInfo, RegionStatus, SeriesPoint, Uptime, Usage } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { BadgeCard } from '../components/BadgeCard'
import { CertificateCard } from '../components/Certificate'
import { formatPeriod, PingLog, PingSetup } from '../components/Heartbeat'
import { RangePicker } from '../components/RangePicker'
import { ResponseChart } from '../components/ResponseChart'
import { StatusBadge } from '../components/StatusBadge'
import { AvgResponseTile, StatTile, UptimeHero } from '../components/Uptime'
import { WorkflowRun } from '../components/WorkflowRun'
import { availableRanges, formatDateTime, formatMs, RANGE_LABELS, reportMonths, reportPdfPath } from '../format'
import { useApi } from '../hooks'
import { formatDuration, IncidentStatus } from './IncidentsPage'

const PAGE = 25

/** One website's monthly PDF report: a month to pick and a download, or a note about which plans include it. */
function ReportDownload({ monitorID, plan }: { monitorID: string; plan: Plan | undefined }) {
  const months = reportMonths(plan?.history_retention_days ?? 7)
  const [month, setMonth] = useState<string | null>(null)
  if (!plan) return null
  if (!plan.reports_enabled) {
    return <p className="hint">A monthly PDF report for this website is included in the Pro and Agency plans. <Link to="/billing">See plans</Link></p>
  }
  // Only a month this page offered is used: the selected value is looked up in our own list, never passed on as typed.
  const chosen = months.find((x) => x.value === month)?.value ?? months[1]?.value ?? months[0].value
  return (
    <div className="row">
      <label htmlFor="report-month">Monthly report</label>
      <select id="report-month" value={chosen} onChange={(e) => setMonth(e.target.value)}>
        {months.map((x) => <option key={x.value} value={x.value}>{x.label}</option>)}
      </select>
      <a className="button" href={reportPdfPath(monitorID, chosen)} rel="noreferrer">Download PDF</a>
      <Link to="/reports">All reports</Link>
    </div>
  )
}

export function MonitorDetailPage() {
  const { monitorID = '' } = useParams()
  const { organization } = useAuth()
  const [range, setRange] = useState('24h')
  const [offset, setOffset] = useState(0)
  const [changed, setChanged] = useState<Monitor | null>(null) // after "Get a new ping address"

  const usage = useApi<{ usage: Usage }>('/usage')
  const monitor = useApi<{ monitor: Monitor }>(`/monitors/${monitorID}`)
  const uptime = useApi<{ uptime: Uptime; range: RangeInfo }>(`/monitors/${monitorID}/uptime?range=${range}`)
  const series = useApi<{ points: SeriesPoint[]; bucket_seconds: number; range: RangeInfo }>(`/monitors/${monitorID}/response-times?range=${range}`)
  const results = useApi<{ results: CheckResult[]; total: number }>(`/monitors/${monitorID}/results?range=${range}&limit=${PAGE}&offset=${offset}`)
  const incidents = useApi<{ incidents: Incident[]; total: number }>(`/monitors/${monitorID}/incidents?limit=5`)
  const locations = useApi<{ regions: RegionStatus[] }>(`/monitors/${monitorID}/regions`)
  // Multi-step API checks: the latest run, whatever the range shown below.
  const latest = useApi<{ results: CheckResult[] }>(`/monitors/${monitorID}/results?range=24h&limit=1`)
  const several = (locations.data?.regions?.length ?? 0) > 1

  if (monitor.error?.status === 404) {
    return (
      <section className="card empty">
        <h1>Monitor not found</h1>
        <p>It may have been deleted. <Link to="/monitors">Back to monitors</Link></p>
      </section>
    )
  }
  const m = changed?.id === monitorID ? changed : monitor.data?.monitor
  const hb = m?.heartbeat ?? null
  const dns = m?.dns ?? null
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
          {hb ? (
            <p className="hint">
              Cron job heartbeat · expected every {formatPeriod(m.effective_check_interval_seconds)} · grace {formatPeriod(hb.grace_seconds)} ·{' '}
              {hb.last_ping_at ? `last ping ${formatDateTime(hb.last_ping_at)}` : 'no ping yet'}
              {hb.due_by && m.enabled && ` · next ping due by ${formatDateTime(hb.due_by)}`}
            </p>
          ) : m.workflow ? (
            <p className="hint">
              Multi-step API check · {m.workflow.steps.length} step{m.workflow.steps.length === 1 ? '' : 's'} · every{' '}
              {Math.round(m.effective_check_interval_seconds / 60)} min ·{' '}
              {m.last_checked_at ? `last run ${formatDateTime(m.last_checked_at)}` : 'not run yet'}
            </p>
          ) : dns ? (
            <p className="hint">
              {dns.record_type} record of {m.url} · {dns.expected ? `must match ${dns.expected}` : 'any answer will do'} · every{' '}
              {Math.round(m.effective_check_interval_seconds / 60)} min ·{' '}
              {m.last_checked_at ? `last checked ${formatDateTime(m.last_checked_at)}` : 'not checked yet'}
            </p>
          ) : (
            <p className="hint">
              {m.method} {m.url} · every {Math.round(m.effective_check_interval_seconds / 60)} min ·{' '}
              {m.last_checked_at ? `last checked ${formatDateTime(m.last_checked_at)}` : 'not checked yet'}
              {m.slow_threshold_ms != null && ` · slow-response alert over ${m.slow_threshold_ms.toLocaleString()} ms`}
            </p>
          )}
          {m.slow_since && m.current_status === 'up' && (
            <Alert tone="warning" role="status">
              Responding slowly since {formatDateTime(m.slow_since)}: responses have been over {m.slow_threshold_ms?.toLocaleString()} ms for{' '}
              {m.slow_checks} checks in a row. The site is online; you will get one alert when response times are back to normal.
            </Alert>
          )}

          {m.workflow?.secrets.some((s) => !s.set) && (
            <Alert tone="warning" role="status">
              This check is paused until its secrets are entered: {m.workflow.secrets.filter((s) => !s.set).map((s) => s.name).join(', ')}.{' '}
              <Link to={`/monitors/${m.id}/edit`}>Enter them</Link>, then resume it.
            </Alert>
          )}
          {m.workflow && (
            <section className="card" aria-labelledby="latest-run-h">
              <h2 id="latest-run-h">Latest run</h2>
              {latest.data?.results[0]?.steps ? (
                <>
                  <p className="hint">
                    {formatDateTime(latest.data.results[0].checked_at)}
                    {latest.data.results[0].response_time_ms != null && ` · ${latest.data.results[0].response_time_ms.toLocaleString()} ms in total`}
                  </p>
                  <WorkflowRun names={m.workflow.steps.map((st) => st.name)} outcomes={latest.data.results[0].steps} label="Latest run" />
                </>
              ) : (
                <p className="hint">The first run shows here a minute or so after the check is added or changed.</p>
              )}
            </section>
          )}

          {hb ? <PingSetup monitor={m} onChanged={setChanged} /> : !dns && <CertificateCard monitor={m} />}

          <ReportDownload monitorID={m.id} plan={usage.data?.usage.plan} />

          <BadgeCard key={m.id} monitor={m} plan={usage.data?.usage.plan} isOwner={organization?.role === 'owner'} />

          <RangePicker value={range} options={options} onChange={changeRange} />

          <div style={{ opacity: loading ? 0.55 : 1 }}>
            {uptime.data && (
              <>
                <UptimeHero uptime={uptime.data.uptime} label={RANGE_LABELS[range] ?? range} />
                {hb ? (
                  <div className="stats">
                    <StatTile label="Pings and missed runs" value={uptime.data.uptime.total_checks.toLocaleString()} />
                    <StatTile label="Missed or failed" value={uptime.data.uptime.failed_checks.toLocaleString()} />
                  </div>
                ) : (
                  <div className="stats">
                    <StatTile label="Checks completed" value={uptime.data.uptime.total_checks.toLocaleString()} />
                    <StatTile label="Failed checks" value={uptime.data.uptime.failed_checks.toLocaleString()} />
                    <AvgResponseTile uptime={uptime.data.uptime} />
                  </div>
                )}
              </>
            )}
          </div>

          {series.data && !hb && (
            <ResponseChart
              title={dns ? 'Average lookup time' : 'Average response time'}
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
            {incidents.data?.incidents.length === 0 && <p>No incidents for this {hb ? 'job' : dns ? 'record' : 'website'}.</p>}
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

          {several && locations.data && (
            <section aria-labelledby="loc-h" className="card">
              <h2 id="loc-h">Locations</h2>
              <p className="hint">The monitor counts as offline only when most locations agree.</p>
              <div className="table-wrap">
                <table>
                  <caption className="sr-only">How each location currently sees this monitor</caption>
                  <thead>
                    <tr><th>Location</th><th>Status</th><th>Last checked</th><th>Latest problem</th></tr>
                  </thead>
                  <tbody>
                    {locations.data.regions.map((r) => (
                      <tr key={r.region}>
                        <td>{r.name}</td>
                        <td>
                          {r.status === 'up' && <span className="d-badge d-badge-soft d-badge-success"><span aria-hidden="true">✔</span> Online</span>}
                          {r.status === 'down' && <span className="d-badge d-badge-soft d-badge-error"><span aria-hidden="true">✖</span> Offline</span>}
                          {r.status === 'unknown' && <span className="d-badge d-badge-soft d-badge-ghost">Waiting</span>}
                        </td>
                        <td>{r.last_checked_at ? formatDateTime(r.last_checked_at) : '—'}</td>
                        <td>{r.status === 'down' ? r.last_error ?? '' : ''}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          {hb && <PingLog monitorID={m.id} refresh={hb.last_ping_at ? Date.parse(hb.last_ping_at) : 0} />}

          <section aria-labelledby="hist-h" style={{ opacity: results.loading ? 0.55 : 1 }}>
            <h2 id="hist-h">{hb ? 'Run history' : 'Check history'}</h2>
            {hb && <p className="hint">Each ping and each missed run, which is what the uptime above is calculated from.</p>}
            {results.data?.results.length === 0 && <p>No {hb ? 'pings' : 'checks'} in this period yet.</p>}
            {results.data && results.data.results.length > 0 && (
              <div className="table-wrap">
                <table>
                  <caption className="sr-only">Individual checks, newest first</caption>
                  <thead>
                    <tr><th>When</th>{several && <th>Location</th>}<th>Result</th>{!hb && <>{!dns && <th className="num">HTTP</th>}<th className="num">{dns ? 'Lookup' : 'Response'}</th></>}<th>Details</th></tr>
                  </thead>
                  <tbody>
                    {results.data.results.map((r) => (
                      <tr key={r.id}>
                        <td><time dateTime={r.checked_at}>{formatDateTime(r.checked_at)}</time></td>
                        {several && <td>{locations.data?.regions.find((x) => x.region === r.region)?.name ?? r.region}</td>}
                        <td>
                          {r.success
                            ? <span className="badge up"><span aria-hidden="true">✔</span> {hb ? 'Ping' : 'Success'}</span>
                            : <span className="badge down"><span aria-hidden="true">✖</span> {hb ? (r.error_category === 'missed_ping' ? 'Missed' : 'Failed') : 'Failed'}</span>}
                        </td>
                        {!hb && (
                          <>
                            {!dns && <td className="num">{r.http_status_code ?? '—'}</td>}
                            <td className="num">{formatMs(r.response_time_ms)}</td>
                          </>
                        )}
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
