import { useEffect } from 'react'
import { Link } from 'react-router'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { CookieSettingsButton } from '../components/CookieBanner'
import { ProductShots } from '../components/ProductShots'
import { Reveal } from '../components/Reveal'
import { usePlans } from '../hooks'
import { PlanCard } from './LandingPage'

// The campaign landing page (/monitoring) for paid ads: one message, one call to action, no site navigation. The copy follows the claims on the home page
// and may only say what the product does today. It is kept out of search results (it repeats the home page) and sets its own title for the ad click.

const BENEFITS = [
  { icon: brand.icons.websites, title: 'Uptime monitoring', text: 'Know when a website or API stops responding.' },
  { icon: brand.icons.analytics, title: 'Response-time tracking', text: 'Spot slowdowns early and review trends over time.' },
  { icon: brand.icons.notifications, title: 'Alerts where you work', text: 'One alert when an outage is confirmed, one when it ends. Email, Slack or webhook.' },
  { icon: brand.icons.ssl, title: 'SSL expiry warnings', text: 'Get warned 30, 14, 7 and 1 days before a certificate expires.' },
]

const AUDIENCES = [
  { title: 'Freelancers', text: 'Know when a client site is offline without refreshing every project.' },
  { title: 'Web agencies', text: 'Keep an eye on every client website from one place.' },
  { title: 'Business owners', text: 'Hear about downtime before your customers do.' },
]

function useCampaignPage() {
  useEffect(() => {
    const meta = document.createElement('meta')
    meta.name = 'robots'
    meta.content = 'noindex'
    document.head.appendChild(meta)
    const title = document.title
    document.title = 'HawkEye: Know Before Your Customers Do'
    return () => {
      meta.remove()
      document.title = title
    }
  }, [])
}

export function MonitoringLandingPage() {
  useCampaignPage()
  const { status } = useAuth()
  const { plans, billingEnabled } = usePlans()
  const signedIn = status === 'authenticated'
  const multiLocation = plans?.some((p) => (p.max_regions ?? 1) > 1) ?? false
  const cta = signedIn ? 'Open dashboard' : 'Start Monitoring Free'
  const ctaTo = signedIn ? '/dashboard' : '/register'

  return (
    <div className="landing mon" data-theme="light">
      <header className="landing-header">
        <Link to="/" aria-label="HawkEye home"><img className="landing-logo" src={brand.logoWhite} alt="HawkEye" width={220} height={43} /></Link>
        <span className="spacer" />
        {!signedIn && <Link to="/login">Log in</Link>}
        <Link className="button primary" to={ctaTo}>{signedIn ? 'Open dashboard' : 'Start free'}</Link>
      </header>

      <section className="mon-hero" aria-labelledby="mon-h1">
        <img className="mon-hero-bg" src={brand.art.hawkEye} alt="" width={1672} height={941} decoding="async" />
        <div className="mon-hero-inner">
          <p className="eyebrow">Website monitoring, without the guesswork</p>
          <div className="mon-hero-copy">
            <h1 id="mon-h1">Know Before <span className="gold">Your <span className="nowrap">Customers Do.</span></span></h1>
            <p>
              HawkEye monitors your websites and APIs, tracks response times, and alerts you when an outage is confirmed, so you can investigate problems before
              your customers have to tell you.
            </p>
            <div className="row">
              <Link className="button primary large" to={ctaTo}>{cta}</Link>
              <a className="button large" href="#pricing-h">View Pricing</a>
            </div>
            <p className="hint">No credit card required · Set up in minutes</p>
          </div>
          <img
            className="mon-hero-laptop"
            src={brand.dashboardLaptop}
            alt="Illustration of a HawkEye dashboard listing monitored websites as online, with an uptime ring and a response-time chart."
            width={566}
            height={463}
          />
        </div>
      </section>

      <section className="section" aria-label="What HawkEye does">
        <Reveal as="ul" className="features mon-benefits">
          {BENEFITS.map((b) => (
            <li key={b.title} className="feature" data-reveal>
              <img src={b.icon} alt="" width={64} height={64} />
              <h3>{b.title}</h3>
              <p>{b.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section className="mon-earth" aria-labelledby="mon-truth-h">
        <img className="mon-earth-bg" src={brand.art.earth} alt="" width={1774} height={887} loading="lazy" decoding="async" />
        <div className="mon-earth-copy">
          <p className="eyebrow">Reduce false alarms</p>
          <h2 id="mon-truth-h">A failed check is <span className="gold">not an incident.</span></h2>
          <p>
            A site counts as offline only after consecutive failed checks, and as recovered only after consecutive successful ones. HawkEye also verifies its own
            connection before reporting an outage: if other trusted websites are unreachable too, it disregards the failed check to help prevent false alarms.
          </p>
          {multiLocation && (
            <p className="mon-earth-note">
              <strong>Checks from several locations.</strong> On paid plans, a monitor is offline only when most locations agree.
            </p>
          )}
        </div>
      </section>

      <ProductShots />

      <section aria-labelledby="mon-audience-h" className="section">
        <h2 id="mon-audience-h">Built for the people who keep the web running</h2>
        <Reveal as="ul" className="features audiences">
          {AUDIENCES.map((a) => (
            <li key={a.title} className="feature" data-reveal>
              <h3>{a.title}</h3>
              <p>{a.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section aria-labelledby="pricing-h" className="section">
        <h2 id="pricing-h">Simple plans. Clear visibility.</h2>
        <p className="hint">Start free. Upgrade when your monitoring needs grow. The free plan is a real plan, not a trial.</p>
        {plans === null && (
          <div role="status" aria-label="Loading plans" className="plans-loading">
            <div className="d-skeleton h-56 w-full" />
            <div className="d-skeleton h-56 w-full" />
            <div className="d-skeleton h-56 w-full" />
          </div>
        )}
        {plans?.length === 0 && <p>Pricing is temporarily unavailable. You can still <Link to="/register">start for free</Link>.</p>}
        {plans && plans.length > 0 && (
          <Reveal as="ul" className="plans">
            {plans.map((p) => (
              <PlanCard key={p.code} plan={p} featured={p.code === 'pro'} billingEnabled={billingEnabled} signedIn={signedIn} />
            ))}
          </Reveal>
        )}
      </section>

      <section className="mon-final" aria-labelledby="mon-final-h">
        <img className="mon-hero-bg" src={brand.art.hawkEye} alt="" width={1672} height={941} loading="lazy" decoding="async" />
        <div className="mon-final-inner">
          <h2 id="mon-final-h">Your website doesn’t clock out. <span className="gold">Neither should your monitoring.</span></h2>
          <div className="row">
            <Link className="button primary large" to={ctaTo}>{cta}</Link>
          </div>
          <p className="hint">No credit card required · Set up in minutes</p>
        </div>
      </section>

      <footer className="mon-footer">
        <span>© {new Date().getFullYear()} HawkEye</span>
        <nav aria-label="Legal">
          <Link to="/privacy">Privacy policy</Link>
          <Link to="/terms">Terms of service</Link>
          <Link to="/contact">Contact us</Link>
          <CookieSettingsButton />
        </nav>
      </footer>
    </div>
  )
}
