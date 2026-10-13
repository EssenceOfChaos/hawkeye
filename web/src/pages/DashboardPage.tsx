import { useState } from 'react'
import { Link } from 'react-router'
import type { DashboardSummary, Incident, RangeInfo, SeriesPoint, Usage } from '../api'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { RangePicker } from '../components/RangePicker'
import { ResponseChart } from '../components/ResponseChart'
import { AvgResponseTile, StatTile, UptimeHero } from '../components/Uptime'
import { availableRanges, RANGE_LABELS } from '../format'
import { useApi } from '../hooks'
import { formatDuration, IncidentStatus } from './IncidentsPage'

export function DashboardPage() {
  const [range, setRange] = useState('24h')
  const usage = useApi<{ usage: Usage }>('/usage')
  const summary = useApi<{ summary: DashboardSummary; range: RangeInfo }>(`/dashboard/summary?range=${range}`)
  const series = useApi<{ points: SeriesPoint[]; bucket_seconds: number; range: RangeInfo }>(`/dashboard/response-times?range=${range}`)
  const incidents = useApi<{ incidents: Incident[] }>('/dashboard/recent-incidents?limit=5')

  const options = availableRanges(usage.data?.usage.plan.history_retention_days ?? 7)
  const s = summary.data?.summary
  const loading = summary.loading || series.loading

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.analytics} />
        <h1>Dashboard</h1>
      </div>

      {(summary.error || series.error) && <p role="alert" className="error">Could not load the dashboard. Try again in a moment.</p>}
      {!s && !summary.error && <p role="status">Loading dashboard…</p>}

      {s && s.monitors.total === 0 && (
        <section className="card empty">
          <h2>Add your first website</h2>
          <p>Add a site and we’ll start checking it right away, then email you if it goes down.</p>
          <Link className="button primary" to="/monitors/new">Add a monitor</Link>
        </section>
      )}

      {s && s.monitors.total > 0 && (
        <>
          {s.open_incidents > 0 && (
            <p role="alert" className="banner inline outage">
              <span aria-hidden="true">✖</span> {s.open_incidents} ongoing incident{s.open_incidents === 1 ? '' : 's'}.{' '}
              <Link to="/incidents">See what’s down</Link>
            </p>
          )}

          <RangePicker value={range} options={options} onChange={setRange} />

          <div style={{ opacity: loading ? 0.55 : 1 }}>
            <UptimeHero uptime={s.uptime} label={RANGE_LABELS[range] ?? range} />

            <h2 className="sr-only">Right now</h2>
            <div className="stats">
              <StatTile label="Up" value={<><span aria-hidden="true">✔</span> {s.monitors.up}</>} hint="monitors responding" />
              <StatTile label="Down" value={<><span aria-hidden="true">✖</span> {s.monitors.down}</>} hint="confirmed outages" />
              <StatTile label="Waiting" value={<><span aria-hidden="true">○</span> {s.monitors.pending}</>} hint="no confirmed state yet" />
              <StatTile label="Paused" value={<><span aria-hidden="true">⏸</span> {s.monitors.paused}</>} hint="not being checked" />
              <AvgResponseTile uptime={s.uptime} />
            </div>
          </div>

          {series.data && (
            <ResponseChart
              title="Average response time, all monitors"
              points={series.data.points}
              bucketSeconds={series.data.bucket_seconds}
              range={series.data.range}
              loading={series.loading}
            />
          )}

          <section aria-labelledby="recent-h" className="card">
            <div className="row">
              <h2 id="recent-h">Recent incidents</h2>
              <span className="spacer" />
              <Link to="/incidents">All incidents</Link>
            </div>
            {incidents.data?.incidents.length === 0 && <p>No incidents. Everything has been healthy.</p>}
            {incidents.data && incidents.data.incidents.length > 0 && (
              <ul className="plain-list">
                {incidents.data.incidents.map((i) => (
                  <li key={i.id}>
                    <IncidentStatus status={i.status} />{' '}
                    <Link to={`/monitors/${i.monitor_id}`}>{i.monitor_name}</Link>
                    <span className="hint">
                      {' '}· {new Date(i.started_at).toLocaleString()} · {formatDuration(i.duration_seconds)}
                      {i.status === 'open' && ' so far'}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </>
  )
}
