import { useEffect, useState } from 'react'
import { useParams } from 'react-router'
import { api, ApiError, type PublicStatus } from '../api'
import { Alert } from '../components/Alert'

const OVERALL: Record<PublicStatus['overall'], string> = {
  operational: '✔ All systems operational',
  partial_outage: '✖ Some systems are experiencing problems',
  major_outage: '✖ Major outage',
  maintenance: '🛠 Scheduled maintenance in progress',
}

const BANNER: Record<PublicStatus['overall'], 'success' | 'error' | 'info'> = {
  operational: 'success',
  partial_outage: 'error',
  major_outage: 'error',
  maintenance: 'info',
}

// Full class names so the CSS generator can see them.
const STATE: Record<PublicStatus['monitors'][number]['state'], { text: string; cls: string }> = {
  up: { text: '✔ Online', cls: 'd-badge d-badge-soft d-badge-success' },
  down: { text: '✖ Offline', cls: 'd-badge d-badge-soft d-badge-error' },
  maintenance: { text: '🛠 Maintenance', cls: 'd-badge d-badge-soft d-badge-info' },
  unknown: { text: 'No recent data', cls: 'd-badge d-badge-soft d-badge-ghost' },
}

const when = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

/** The public, no-login status page for /status/<slug>. */
export function StatusPage() {
  const { slug = '' } = useParams()
  const [data, setData] = useState<PublicStatus | null>(null)
  const [state, setState] = useState<'loading' | 'missing' | 'error' | 'ok'>('loading')

  useEffect(() => {
    let live = true
    api<PublicStatus>('GET', `/public/status/${encodeURIComponent(slug)}`)
      .then((d) => {
        if (!live) return
        setData(d)
        setState('ok')
      })
      .catch((e) => {
        if (live) setState(e instanceof ApiError && e.status === 404 ? 'missing' : 'error')
      })
    return () => {
      live = false
    }
  }, [slug])

  useEffect(() => {
    if (data) document.title = `${data.title} status`
  }, [data])

  const uptimes = data?.monitors.map((m) => m.uptime_percent).filter((u): u is number => u !== null) ?? []
  const average = uptimes.length ? uptimes.reduce((a, b) => a + b, 0) / uptimes.length : null

  if (state === 'loading') return <p role="status" className="center"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>
  if (state === 'missing') return <main className="status-wrap"><h1>Status page not found</h1><p>Check the address, or ask the site’s owner for the right link.</p></main>
  if (state === 'error' || !data) return <main className="status-wrap"><Alert>The status could not be loaded. Try again in a moment.</Alert></main>

  return (
    <main className="status-wrap">
      <section className="d-hero status-hero">
        <div className="d-hero-content">
          <div>
            <h1>{data.title}</h1>
            {data.description && <p>{data.description}</p>}
          </div>
        </div>
      </section>
      <Alert tone={BANNER[data.overall]} role="status">
        <strong>{OVERALL[data.overall]}</strong>
      </Alert>

      <div className="stats-row" aria-label="Summary">
        <div className="d-stats">
          <div className="d-stat">
            <div className="d-stat-title">Services operational</div>
            <div className="d-stat-value">{data.monitors.filter((m) => m.state === 'up').length} of {data.monitors.length}</div>
          </div>
        </div>
        <div className="d-stats">
          <div className="d-stat">
            <div className="d-stat-title">Average uptime</div>
            <div className="d-stat-value">{average === null ? '—' : `${average.toFixed(2)}%`}</div>
            <div className="d-stat-desc">Last {data.history_days} days</div>
          </div>
        </div>
        <div className="d-stats">
          <div className="d-stat">
            <div className="d-stat-title">Ongoing incidents</div>
            <div className="d-stat-value">{data.incidents.filter((i) => !i.resolved_at).length}</div>
          </div>
        </div>
      </div>

      {data.monitors.map((m) => {
        const known = m.days.filter((d) => d.status !== 'none')
        return (
          <section key={m.name} className="status-row" aria-label={m.name}>
            <div className="head">
              <span>{m.name}</span>
              <span><span className={STATE[m.state].cls}>{STATE[m.state].text}</span>{m.uptime_percent !== null && <span className="hint"> · {m.uptime_percent.toFixed(2)}% uptime</span>}</span>
            </div>
            <div className="status-bars" role="img"
              aria-label={`Daily uptime for the last ${data.history_days} days: ${known.length} days with data, ${known.filter((d) => d.status === 'down').length} days with an outage`}>
              {m.days.map((d) => (
                <span key={d.date} className="d-tooltip" data-tip={`${d.date}: ${d.uptime_percent === null ? 'no data' : `${d.uptime_percent.toFixed(2)}% uptime`}`}>
                  <span className={d.status} />
                </span>
              ))}
            </div>
            <div className="scale" aria-hidden="true"><span>{data.history_days} days ago</span><span>Today</span></div>
          </section>
        )
      })}

      <h2>Recent incidents</h2>
      {data.incidents.length === 0 ? (
        <p className="hint">No incidents in the last 14 days.</p>
      ) : (
        <ul>
          {data.incidents.map((i) => (
            <li key={`${i.monitor}-${i.started_at}`}>
              <strong>{i.monitor}</strong> — {i.resolved_at ? `unavailable ${when(i.started_at)}, resolved ${when(i.resolved_at)}` : `unavailable since ${when(i.started_at)} (ongoing)`}
            </li>
          ))}
        </ul>
      )}
      <p className="hint">Updated {when(data.generated_at)} · Powered by <a href="/">HawkEye</a></p>
    </main>
  )
}
