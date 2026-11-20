import { useEffect, useState } from 'react'
import type { PublicStatus } from '../api'
import { Alert } from '../components/Alert'
import { UpdateStatusBadge } from '../components/UpdateStatusBadge'
import { StatusNotFound } from '../statusLoad'
import { isHexColor, readableTextOn } from '../color'
import { statusUptime } from '../format'

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

/** The host of the client's website, for the "Back to …" link. */
function hostOf(url: string): string {
  try {
    return new URL(url).host.replace(/^www\./, '')
  } catch {
    return url
  }
}

/** The browser tab's title: the page's own, with "status" added unless it already says so ("Acme status", not "Acme status status"). */
function tabTitle(title: string): string {
  return /\bstatus\s*$/i.test(title) ? title.trim() : `${title} status`
}

/**
 * Which edge a day's tooltip lines up with. Centered over a bar near either end of the row, the text would stick out past the page and make a phone
 * scroll sideways, so the first third open toward the right and the last third toward the left.
 */
function tipEdge(i: number, n: number): string {
  if (i < n / 3) return ' tip-start'
  if (i >= (2 * n) / 3) return ' tip-end'
  return ''
}

/** "Powered by HawkEye" links to HawkEye's own address on a customer's domain, and to this site's home page otherwise. */
function homeLink(home: string | undefined): string {
  return home && /^https?:\/\//.test(home) ? home : '/'
}

const when = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

/**
 * A public status page, with no login and nothing else of the app: shown at /status/<slug> (StatusPage) and on customers' own domains
 * (src/status-main.tsx). `load` fetches it, again whenever it changes, so callers keep it stable (useCallback). `onLoaded` sees the page first
 * and returns false to show nothing (StatusPage sends visitors to the page's own domain instead).
 */
export function StatusView({ load, onLoaded }: { load: () => Promise<PublicStatus>; onLoaded?: (d: PublicStatus) => boolean }) {
  const [data, setData] = useState<PublicStatus | null>(null)
  const [state, setState] = useState<'loading' | 'missing' | 'error' | 'ok' | 'away'>('loading')

  useEffect(() => {
    let live = true
    load()
      .then((d) => {
        if (!live) return
        if (onLoaded && !onLoaded(d)) {
          setState('away')
          return
        }
        setData(d)
        setState('ok')
      })
      .catch((e) => {
        if (live) setState(e instanceof StatusNotFound ? 'missing' : 'error')
      })
    return () => {
      live = false
    }
  }, [load, onLoaded])

  useEffect(() => {
    if (data) document.title = tabTitle(data.title)
  }, [data])

  // Search engines index the page once, at its own domain when it has one.
  useEffect(() => {
    if (!data?.custom_domain_url?.startsWith('https://')) return
    const link = document.createElement('link')
    link.rel = 'canonical'
    link.href = `${data.custom_domain_url}/`
    document.head.querySelectorAll('link[rel="canonical"]').forEach((l) => l.remove())
    document.head.appendChild(link)
    return () => link.remove()
  }, [data])

  const uptimes = data?.monitors.map((m) => m.uptime_percent).filter((u): u is number => u !== null) ?? []
  const average = uptimes.length ? uptimes.reduce((a, b) => a + b, 0) / uptimes.length : null

  if (state === 'away') return <p role="status" className="center">Opening the status page…</p>
  if (state === 'loading') return <p role="status" className="center"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>
  if (state === 'missing') return <main className="status-wrap"><h1>Status page not found</h1><p>Check the address, or ask the site’s owner for the right link.</p></main>
  if (state === 'error' || !data) return <main className="status-wrap"><Alert>The status could not be loaded. Try again in a moment.</Alert></main>

  const brand = data.branding ?? { powered_by: true }
  const accent = brand.accent_color && isHexColor(brand.accent_color) ? brand.accent_color : null
  // The server only sends an https address, but check again before it becomes a link.
  const website = brand.website_url?.startsWith('https://') ? brand.website_url : null
  return (
    <main className="status-wrap">
      {website && (
        <p className="status-back"><a href={website} rel="noopener noreferrer nofollow">← Back to {hostOf(website)}</a></p>
      )}
      <section className={`d-hero status-hero${accent ? ' branded' : ''}`} style={accent ? { background: accent, color: readableTextOn(accent) } : undefined}>
        <div className="d-hero-content">
          {brand.logo_url && <img className="status-logo" src={brand.logo_url} alt="" />}
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
            <div className="d-stat-value">{average === null ? '—' : statusUptime(average)}</div>
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
              <span><span className={STATE[m.state].cls}>{STATE[m.state].text}</span>{m.uptime_percent !== null && <span className="hint"> · {statusUptime(m.uptime_percent)} uptime</span>}</span>
            </div>
            <div className="status-bars" role="img"
              aria-label={`Daily uptime for the last ${data.history_days} days: ${known.length} days with data, ${known.filter((d) => d.status === 'down').length} days with an outage`}>
              {m.days.map((d, i) => (
                <span key={d.date} className={`d-tooltip${tipEdge(i, m.days.length)}`} data-tip={`${d.date}: ${d.uptime_percent === null ? 'no data' : `${statusUptime(d.uptime_percent)} uptime`}`}>
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
        <ul className="incident-list">
          {data.incidents.map((i) => (
            <li key={`${i.monitor}-${i.started_at}`}>
              <strong>{i.monitor}</strong> — {i.resolved_at ? `unavailable ${when(i.started_at)}, resolved ${when(i.resolved_at)}` : `unavailable since ${when(i.started_at)} (ongoing)`}
              {/* Plain text from the site's owner: React shows it as text, so nothing in it can become markup or a link. */}
              {(i.updates ?? []).length > 0 && (
                <ol className="update-list" aria-label={`Updates on ${i.monitor}, newest first`}>
                  {(i.updates ?? []).map((u) => (
                    <li key={`${u.posted_at}-${u.status}`}>
                      <div className="update-head"><UpdateStatusBadge status={u.status} /><time dateTime={u.posted_at}>{when(u.posted_at)}</time></div>
                      <p className="update-message">{u.message}</p>
                    </li>
                  ))}
                </ol>
              )}
            </li>
          ))}
        </ul>
      )}
      <p className="hint">Updated {when(data.generated_at)}{brand.powered_by && <> · Powered by <a href={homeLink(data.home_url)}>HawkEye</a></>}</p>
    </main>
  )
}
