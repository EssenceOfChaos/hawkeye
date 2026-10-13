import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, type Plan } from '../api'
import { useAuth } from '../auth'
import { brand } from '../brand'

const FEATURES = [
  {
    icon: brand.icons.websites,
    title: 'Watch every client site',
    text: 'Add all the websites you look after and see which are up at a glance. Checks run automatically, around the clock.',
  },
  {
    icon: brand.icons.email,
    title: 'Alerts you can trust',
    text: 'We confirm an outage with consecutive failed checks before emailing you, then tell you when the site is back. No false alarms from one-off blips.',
  },
  {
    icon: brand.icons.history,
    title: 'Incident history',
    text: 'Every outage is recorded with when it started, how long it lasted and the likely cause, so you can answer "what happened?" with facts.',
  },
  {
    icon: brand.icons.settings,
    title: 'Alert the right people',
    text: 'Send alerts to you, your team or your client. Addresses must be confirmed by their owner before they receive anything.',
  },
  {
    icon: brand.icons.shield,
    title: 'Built to be safe',
    text: 'Checks are hardened against being pointed at private networks, and each customer’s data is strictly isolated.',
  },
  {
    icon: brand.icons.analytics,
    title: 'Uptime and response-time reports',
    text: 'Charts of response times and check-based uptime per client website.',
    soon: true,
  },
]

function price(cents: number) {
  return cents === 0 ? '$0' : `$${(cents / 100).toFixed(0)}`
}

function PlanCard({ plan, featured, billingEnabled, signedIn }: { plan: Plan; featured: boolean; billingEnabled: boolean; signedIn: boolean }) {
  const mins = Math.round(plan.minimum_check_interval_seconds / 60)
  return (
    <li className={`plan${featured ? ' featured' : ''}`}>
      <h3>{plan.name}</h3>
      <p className="price">
        <strong>{price(plan.monthly_price_cents)}</strong>
        {plan.monthly_price_cents > 0 && <span> / month</span>}
      </p>
      <ul>
        <li>{plan.max_monitors} website monitors</li>
        <li>Checks every {mins} minute{mins === 1 ? '' : 's'}</li>
        <li>{plan.history_retention_days} days of history</li>
        <li>Email alerts for outages and recoveries</li>
      </ul>
      {plan.monthly_price_cents === 0 ? (
        <Link className="button primary" to="/register">Start free</Link>
      ) : billingEnabled ? (
        <Link className="button primary" to={signedIn ? '/billing' : '/register'}>{signedIn ? `Choose ${plan.name}` : `Start free, upgrade to ${plan.name}`}</Link>
      ) : (
        <button disabled>Paid plans open soon</button>
      )}
    </li>
  )
}

export function LandingPage() {
  const { status } = useAuth()
  const [plans, setPlans] = useState<Plan[] | null>(null)
  const [billingEnabled, setBillingEnabled] = useState(false)

  useEffect(() => {
    let cancelled = false
    api<{ plans: Plan[]; billing_enabled?: boolean }>('GET', '/plans')
      .then((r) => {
        if (cancelled) return
        setPlans(r.plans)
        setBillingEnabled(Boolean(r.billing_enabled))
      })
      .catch(() => !cancelled && setPlans([]))
    return () => {
      cancelled = true
    }
  }, [])

  const signedIn = status === 'authenticated'

  return (
    <div className="landing">
      <header className="landing-header">
        <img className="landing-logo" src={brand.logo} alt="HawkEye" width={150} height={54} />
        <span className="spacer" />
        {signedIn ? (
          <Link className="button primary" to="/dashboard">Open dashboard</Link>
        ) : (
          <>
            <Link to="/login">Log in</Link>
            <Link className="button primary" to="/register">Start free</Link>
          </>
        )}
      </header>

      <section className="hero">
        <div className="hero-text">
          <h1>Know when a client’s website goes down — before they do.</h1>
          <p>
            HawkEye monitors all of your clients’ websites from one place, emails you when one is really down and when it
            recovers, and keeps a record of every incident.
          </p>
          <div className="row">
            <Link className="button primary large" to={signedIn ? '/dashboard' : '/register'}>
              {signedIn ? 'Open dashboard' : 'Monitor 5 sites free'}
            </Link>
            <span className="hint">No credit card needed.</span>
          </div>
        </div>
        <img
          className="hero-art"
          src={brand.dashboardLaptop}
          alt="Illustration of a HawkEye dashboard listing monitored websites as online, with an uptime ring and a response-time chart."
          width={566}
          height={463}
        />
      </section>

      <section aria-labelledby="features-h" className="section">
        <h2 id="features-h">Everything a small agency needs to stay on top of uptime</h2>
        <ul className="features">
          {FEATURES.map((f) => (
            <li key={f.title} className="feature">
              <img src={f.icon} alt="" width={64} height={64} />
              <h3>
                {f.title}
                {f.soon && <span className="soon">Coming soon</span>}
              </h3>
              <p>{f.text}</p>
            </li>
          ))}
        </ul>
      </section>

      <section className="section split">
        <img src={brand.websiteMagnifier} alt="" width={180} height={196} />
        <div>
          <h2>Checks that tell the truth</h2>
          <p>
            A site counts as down only after consecutive failed checks, and as recovered only after consecutive successful ones. You
            get one email when an outage is confirmed and one when it ends — not a flood.
          </p>
        </div>
        <img src={brand.alertBell} alt="" width={140} height={152} />
      </section>

      <section aria-labelledby="pricing-h" className="section">
        <h2 id="pricing-h">Simple pricing</h2>
        <p className="hint">The free plan is a real plan, not a trial. Monitors are never deleted if you move to a smaller plan.</p>
        {plans === null && <p role="status">Loading plans…</p>}
        {plans?.length === 0 && <p>Pricing is temporarily unavailable. You can still <Link to="/register">start for free</Link>.</p>}
        {plans && plans.length > 0 && (
          <ul className="plans">
            {plans.map((p) => (
              <PlanCard key={p.code} plan={p} featured={p.code === 'pro'} billingEnabled={billingEnabled} signedIn={signedIn} />
            ))}
          </ul>
        )}
      </section>

      <section className="section split trust">
        <img src={brand.cloudSecurity} alt="" width={232} height={192} />
        <div>
          <h2>Your clients’ trust, protected</h2>
          <p>
            Passwords are hashed with Argon2id, sessions are protected against cross-site attacks, each organization’s data is kept
            separate, and checks cannot be used to probe private networks.
          </p>
        </div>
      </section>

      <footer className="landing-footer">
        <img src={brand.appIcon} alt="" width={28} height={28} />
        <span>© HawkEye</span>
      </footer>
    </div>
  )
}
