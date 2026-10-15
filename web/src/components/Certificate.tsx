import { useEffect, useState, type CSSProperties } from 'react'
import { Link } from 'react-router'
import type { CertificateAlert, Monitor, SSLInfo } from '../api'

/** Warning milestones, in days before expiry. Mirrors checks.SSLBuckets on the server. */
const MILESTONES = [30, 14, 7, 1] as const
const DAY = 86_400_000

/** The current time, refreshed every `everyMs`, so a countdown keeps moving while the page stays open. */
function useNow(everyMs = 60_000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), everyMs)
    return () => clearInterval(id)
  }, [everyMs])
  return now
}

function split(msLeft: number) {
  const mins = Math.max(0, Math.floor(msLeft / 60_000))
  return { days: Math.floor(mins / 1440), hours: Math.floor((mins % 1440) / 60), minutes: mins % 60 }
}

export function certTone(msLeft: number): 'ok' | 'soon' | 'expired' {
  return msLeft <= 0 ? 'expired' : msLeft <= 30 * DAY ? 'soon' : 'ok'
}

const TONE_TEXT = { ok: 'Valid', soon: 'Expiring soon', expired: 'Expired' } as const

/** One countdown figure (days, hours or minutes) using daisyUI's countdown component. */
function Digit({ value, label }: { value: number; label: string }) {
  return (
    <div className="cert-digit">
      <span className="d-countdown">
        <span style={{ '--d-value': Math.min(value, 999), '--d-digits': 2 } as CSSProperties} aria-hidden="true">{value}</span>
      </span>
      <span className="stat-label">{label}</span>
    </div>
  )
}

/** A compact certificate status for lists: "42 days", "5 days", "Expired". Renders nothing useful for plain-HTTP monitors. */
export function CertBadge({ monitor }: { monitor: Pick<Monitor, 'url' | 'ssl'> }) {
  const now = useNow()
  if (!monitor.url.toLowerCase().startsWith('https://')) return <span className="hint">No SSL</span>
  if (!monitor.ssl) return <span className="hint">Not checked yet</span>
  const left = new Date(monitor.ssl.expires_at).getTime() - now
  const tone = certTone(left)
  const days = Math.floor(left / DAY)
  const text = tone === 'expired' ? 'Expired' : days >= 1 ? `${days} day${days === 1 ? '' : 's'}` : 'Under a day'
  return (
    <span className={`d-badge cert-badge cert-${tone}`} title={`Certificate expires ${new Date(monitor.ssl.expires_at).toLocaleString()}`}>
      <span aria-hidden="true">{tone === 'ok' ? '🔒' : tone === 'soon' ? '⚠' : '✖'}</span> {text}
      <span className="sr-only"> until the SSL certificate expires ({TONE_TEXT[tone]})</span>
    </span>
  )
}

/** The monitor page's certificate panel: a live countdown and the schedule of warning emails as a timeline. */
export function CertificateCard({ monitor }: { monitor: Pick<Monitor, 'url' | 'ssl'> }) {
  const now = useNow()
  const isHttps = monitor.url.toLowerCase().startsWith('https://')
  return (
    <section className="card cert-card" aria-labelledby="cert-h">
      <h2 id="cert-h">SSL certificate</h2>
      {!isHttps && <p className="hint">This monitor uses http://, so there is no certificate to track.</p>}
      {isHttps && !monitor.ssl && <p className="hint" role="status">HawkEye reads the certificate on the next check. Its expiry date will appear here.</p>}
      {isHttps && monitor.ssl && <CertificateDetails ssl={monitor.ssl} now={now} />}
    </section>
  )
}

function CertificateDetails({ ssl, now }: { ssl: SSLInfo; now: number }) {
  const expires = new Date(ssl.expires_at)
  const left = expires.getTime() - now
  const tone = certTone(left)
  const { days, hours, minutes } = split(left)
  const daysLeft = left / DAY
  return (
    <div className="cert-layout">
      <div>
        <p className={`cert-status cert-${tone}`}>{TONE_TEXT[tone]}</p>
        {tone === 'expired' ? (
          <p className="cert-expired">
            Expired {Math.max(1, Math.floor(-left / DAY)) === 1 && -left < DAY ? 'today' : `${Math.floor(-left / DAY)} days ago`} · {expires.toLocaleDateString()}
          </p>
        ) : (
          <>
            <div className="cert-digits" role="timer" aria-label={`${days} days, ${hours} hours and ${minutes} minutes until the certificate expires`}>
              <Digit value={days} label="days" />
              <Digit value={hours} label="hours" />
              <Digit value={minutes} label="min" />
            </div>
            <p className="hint">Expires {expires.toLocaleString()}</p>
          </>
        )}
        <p className="hint">Last read {new Date(ssl.checked_at).toLocaleString()}</p>
      </div>
      <div>
        <h3 className="cert-timeline-h">Warning emails</h3>
        <ul className="d-timeline d-timeline-vertical d-timeline-compact cert-timeline">
          {MILESTONES.map((m) => {
            const reached = daysLeft <= m
            return (
              <li key={m} className={reached ? 'reached' : ''}>
                <hr />
                <div className="d-timeline-middle" aria-hidden="true">{reached ? '✔' : '○'}</div>
                <div className="d-timeline-end">
                  <strong>{m === 1 ? '1 day' : `${m} days`} before</strong>
                  <span className="hint"> {reached ? 'sent, or due on the next check' : 'upcoming'}</span>
                </div>
                <hr />
              </li>
            )
          })}
          <li className={tone === 'expired' ? 'reached expired' : ''}>
            <hr />
            <div className="d-timeline-middle" aria-hidden="true">{tone === 'expired' ? '✖' : '○'}</div>
            <div className="d-timeline-end">
              <strong>Expires {expires.toLocaleDateString()}</strong>
              <span className="hint"> {tone === 'expired' ? 'alert sent, or due on the next check' : 'final alert if it lapses'}</span>
            </div>
          </li>
        </ul>
      </div>
    </div>
  )
}

/** Dashboard panel listing certificates that need attention. */
export function CertificatesPanel({ certificates, tracked }: { certificates: CertificateAlert[]; tracked: number }) {
  if (tracked === 0) return null
  return (
    <section className="card" aria-labelledby="certs-h">
      <h2 id="certs-h">SSL certificates</h2>
      {certificates.length === 0 ? (
        <p>All {tracked} tracked certificate{tracked === 1 ? '' : 's'} {tracked === 1 ? 'has' : 'have'} more than 30 days left. 🔒</p>
      ) : (
        <ul className="plain-list cert-list">
          {certificates.map((c) => {
            const expired = c.status === 'expired'
            return (
              <li key={c.monitor_id}>
                <Link to={`/monitors/${c.monitor_id}`}>{c.monitor_name}</Link>
                <span className={`d-badge cert-badge cert-${expired ? 'expired' : 'soon'}`}>
                  {expired ? 'Expired' : c.days_remaining < 1 ? 'Under a day' : `${c.days_remaining} day${c.days_remaining === 1 ? '' : 's'}`}
                </span>
                <span className="hint"> {expired ? 'expired' : 'expires'} {new Date(c.expires_at).toLocaleDateString()}</span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
