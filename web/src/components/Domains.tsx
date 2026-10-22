import { Link } from 'react-router'
import type { DomainAlert, TrackedDomain } from '../api'

const DAY = 86_400_000

/** "42 days", "Under a day", "Expired" for a domain's remaining time. */
export function domainLeft(expiresAt: string, now = Date.now()): string {
  const left = new Date(expiresAt).getTime() - now
  if (left <= 0) return 'Expired'
  const days = Math.floor(left / DAY)
  return days >= 1 ? `${days} day${days === 1 ? '' : 's'}` : 'Under a day'
}

/** A domain's state as a badge. Domains with no known expiry say so in words: "Expiry unavailable" is not a warning. */
export function DomainBadge({ domain }: { domain: Pick<TrackedDomain, 'status' | 'tracked' | 'expires_at'> }) {
  if (!domain.tracked) return <span className="d-badge d-badge-ghost">Not tracked</span>
  switch (domain.status) {
    case 'pending':
      return <span className="hint">Not looked up yet</span>
    case 'unavailable':
      return <span className="d-badge d-badge-ghost">Expiry unavailable</span>
    case 'expired':
      return <span className="d-badge cert-badge cert-expired"><span aria-hidden="true">✖</span> Expired</span>
    case 'expiring':
      return <span className="d-badge cert-badge cert-soon"><span aria-hidden="true">⚠</span> Expiring soon</span>
    default:
      return <span className="d-badge cert-badge cert-ok"><span aria-hidden="true">✔</span> Valid</span>
  }
}

/** Dashboard panel listing domain registrations that need attention. */
export function DomainsPanel({ domains, tracked }: { domains: DomainAlert[]; tracked: number }) {
  if (tracked === 0) return null
  return (
    <section className="card" aria-labelledby="domains-h">
      <div className="row">
        <h2 id="domains-h">Domain registrations</h2>
        <span className="spacer" />
        <Link to="/domains">All domains</Link>
      </div>
      {domains.length === 0 ? (
        <p>All {tracked} tracked domain{tracked === 1 ? '' : 's'} {tracked === 1 ? 'has' : 'have'} more than 30 days left. 🌐</p>
      ) : (
        <ul className="plain-list cert-list">
          {domains.map((d) => {
            const expired = d.status === 'expired'
            return (
              <li key={d.domain_id}>
                <Link to="/domains">{d.name}</Link>
                <span className={`d-badge cert-badge cert-${expired ? 'expired' : 'soon'}`}>{domainLeft(d.expires_at)}</span>
                <span className="hint"> {expired ? 'expired' : 'expires'} {new Date(d.expires_at).toLocaleDateString()}</span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
