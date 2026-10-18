import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { api, type Plan } from '../api'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { Reveal } from '../components/Reveal'
import { inScope, parallax, prefersReducedMotion, rotateScramble } from '../motion'

// Simple Icons paths (CC0), 24x24.
const SOCIALS = [
  { name: 'X', href: 'https://x.com/HawkEyeObs', path: 'M14.234 10.162 22.977 0h-2.072l-7.591 8.824L7.251 0H.258l9.168 13.343L.258 24H2.33l8.016-9.318L16.749 24h6.993zm-2.837 3.299-.929-1.329L3.076 1.56h3.182l5.965 8.532.929 1.329 7.754 11.09h-3.182z' },
  { name: 'Facebook', href: 'https://www.facebook.com/people/HawkEye/61595561066921/', path: 'M9.101 23.691v-7.98H6.627v-3.667h2.474v-1.58c0-4.085 1.848-5.978 5.858-5.978.401 0 .955.042 1.468.103a8.68 8.68 0 0 1 1.141.195v3.325a8.623 8.623 0 0 0-.653-.036 26.805 26.805 0 0 0-.733-.009c-.707 0-1.259.096-1.675.309a1.686 1.686 0 0 0-.679.622c-.258.42-.374.995-.374 1.752v1.297h3.919l-.386 2.103-.287 1.564h-3.246v8.245C19.396 23.238 24 18.179 24 12.044c0-6.627-5.373-12-12-12s-12 5.373-12 12c0 5.628 3.874 10.35 9.101 11.647Z' },
  { name: 'LinkedIn', href: 'https://www.linkedin.com/company/hawkeye-observer/', path: 'M20.447 20.452h-3.554v-5.569c0-1.328-.027-3.037-1.852-3.037-1.853 0-2.136 1.445-2.136 2.939v5.667H9.351V9h3.414v1.561h.046c.477-.9 1.637-1.85 3.37-1.85 3.601 0 4.267 2.37 4.267 5.455v6.286zM5.337 7.433a2.062 2.062 0 0 1-2.063-2.065 2.064 2.064 0 1 1 2.063 2.065zm1.782 13.019H3.555V9h3.564v11.452zM22.225 0H1.771C.792 0 0 .774 0 1.729v20.542C0 23.227.792 24 1.771 24h20.451C23.2 24 24 23.227 24 22.271V1.729C24 .774 23.2 0 22.222 0h.003z' },
]

const PHRASES = ['watches your websites.', 'alerts you to incidents.', 'tracks your uptime.', 'measures response times.']

const PROBLEMS = [
  { title: 'The outage you didn’t see coming', text: 'Hear it from HawkEye, not from a customer.' },
  { title: 'The slowdown hiding in plain sight', text: 'Slow responses are an early warning. Spot them sooner.' },
  { title: 'The growing list of websites', text: 'Ten sites or a hundred: one dashboard shows what needs attention.' },
]

const FEATURES = [
  {
    icon: brand.icons.websites,
    title: 'Website and API monitoring',
    lead: 'Know when a website or API stops responding.',
    text: 'Send custom requests, authenticate, and verify what comes back.',
  },
  {
    icon: brand.icons.analytics,
    title: 'Response-time tracking',
    lead: 'Spot slowdowns early.',
    text: 'Track response times and review trends over time.',
  },
  {
    icon: brand.icons.history,
    title: 'Incident tracking',
    lead: 'Every incident, clearly accounted for.',
    text: 'See when a problem began and when service recovered.',
  },
  {
    icon: brand.icons.notifications,
    title: 'Alerts where you work',
    lead: 'The right signal. When it matters.',
    text: 'One alert when an outage is confirmed, one when it ends. Email, Slack or webhook.',
  },
  {
    icon: brand.icons.websites,
    title: 'Public status page',
    lead: 'Tell your customers before they ask.',
    text: 'Share live status, uptime and incidents. You choose what appears.',
  },
  {
    icon: brand.icons.maintenance,
    title: 'Maintenance windows',
    lead: 'Planned work shouldn’t page you.',
    text: 'Pause alerts during deploys. Included in Pro and Agency.',
  },
  {
    icon: brand.icons.ssl,
    title: 'SSL certificate alerts',
    lead: 'Never be surprised by an expired certificate.',
    text: 'A live countdown, plus warnings 30, 14, 7 and 1 days before expiry.',
  },
  {
    icon: brand.icons.dashboard,
    title: 'One dashboard, clear reports',
    lead: 'Every website. One clear picture.',
    text: 'Availability, response times, incidents and certificates in one place.',
  },
]

// Shown only when this installation really offers more than one location (the plans report how many each allows).
const LOCATION_FEATURE = {
  icon: brand.icons.shield,
  title: 'Checks from several locations',
  lead: 'One network hiccup is not an outage.',
  text: 'On paid plans, a monitor is down only when most locations agree.',
}
const LOCATION_FAQ = {
  q: 'Can HawkEye check from more than one location?',
  a: 'Yes, on paid plans. Pick the locations for each monitor; HawkEye checks from all of them and raises an incident only when most of them see a failure, which avoids false alarms from a problem at one location. The monitor page shows how each location sees your site. Free monitors are checked from the primary location.',
}

const AUDIENCES = [
  { title: 'Freelancers', lead: 'Spend less time checking. More time creating.', text: 'Know when a client site is down without refreshing every project.' },
  { title: 'Web agencies', lead: 'Keep an eye on every client website.', text: 'Monitor every client in one place and show the care you provide.' },
  { title: 'Independent business owners', lead: 'Your website is too important to leave unchecked.', text: 'Hear about downtime before your customers do.' },
  { title: 'Developers and small teams', lead: 'Make availability visible.', text: 'Check your APIs too: authenticated requests and JSON checks, no complex stack.' },
]

const STEPS = [
  { title: 'Add your website', text: 'Enter a name and URL. It joins your dashboard.' },
  { title: 'Let HawkEye keep watch', text: 'Checked at your plan’s interval, with every result recorded.' },
  { title: 'Get notified when something changes', text: 'Confirmed failures open an incident and alert you by email, Slack or webhook.' },
  { title: 'Investigate and move forward', text: 'Review the incident. When checks recover, HawkEye resolves it and tells you.' },
]

const FAQ = [
  { q: 'How does website monitoring work?', a: 'You add a website URL. HawkEye periodically sends an HTTP request, records the result, and evaluates whether repeated failures indicate a potential outage.' },
  { q: 'Do I need a credit card?', a: 'No. The free plan does not ask for one. A payment method is only needed if you choose a paid plan.' },
  { q: 'What happens when my website goes down?', a: 'When repeated failures meet the incident threshold, HawkEye creates an incident and sends an alert to your email, Slack or webhook destinations. It keeps checking, and tells you when the website recovers.' },
  { q: 'Will HawkEye guarantee that my website is always available?', a: 'No monitoring service can guarantee that a website will always be available or detect every possible problem. HawkEye provides periodic checks and recorded results to help you identify and investigate availability issues.' },
  { q: 'Does HawkEye warn me before my SSL certificate expires?', a: 'Yes, on every plan. For each HTTPS monitor, HawkEye reads the certificate your site presents during its checks, shows the expiry date and a countdown, and alerts your destinations 30, 14, 7 and 1 days before it expires and again if it does. Renew the certificate and HawkEye picks up the new date automatically. It reports the certificate it sees for the monitored address; it cannot tell you about other hostnames or certificates revoked early.' },
  { q: 'Where can HawkEye send alerts?', a: 'To email addresses, to a Slack channel through an incoming webhook, and to any HTTPS endpoint as a signed JSON webhook so your own systems can react. Every alert is queued and retried if delivery fails. Slack and webhook destinations are tested when you add them and available on every plan. SMS is not available yet.' },
  { q: 'Can I show my customers a status page?', a: 'Yes, on every plan. Choose an address, the monitors to show and the names to show them under, then publish. The page shows each service’s current state, daily uptime and recent incidents. It never shows your monitored web addresses or error details, and it stays private until you publish it. Daily history follows your plan: 7 days on Free, up to 90 on paid plans.' },
  { q: 'How do I avoid false alerts during planned maintenance?', a: 'Schedule a maintenance window on a Pro or Agency plan. For all or some of your monitors, once or repeating daily or weekly, HawkEye keeps checking but does not open incidents or send alerts, and leaves those checks out of uptime. An incident that is already open stays open until the monitor recovers.' },
  { q: 'Can I monitor an API?', a: 'Yes. Choose the request method, add custom headers and a request body, and authenticate with Basic, a bearer token or an API-key header. You can then check the status code, look for text in the response, or verify a value in a JSON response. Passwords, tokens and API keys are stored encrypted and are never shown again. Methods that change data (POST, PUT, PATCH and DELETE) are repeated on every check, so they require a verified email address and run at most every 5 minutes.' },
  { q: 'What happens if I downgrade?', a: 'Your existing monitors are never deleted automatically. You may not be able to add new ones until you are within the new plan’s limits, and history older than the new plan’s retention period is removed. Maintenance windows you already created keep working, but you cannot create new ones until you are on a paid plan.' },
]

function price(cents: number) {
  return cents === 0 ? '$0' : `$${(cents / 100).toFixed(0)}`
}

function PlanCard({ plan, featured, billingEnabled, signedIn }: { plan: Plan; featured: boolean; billingEnabled: boolean; signedIn: boolean }) {
  const mins = Math.round(plan.minimum_check_interval_seconds / 60)
  const card = (
    <div className={`plan${featured ? ' featured' : ''}`}>
      <h3>{plan.name}</h3>
      <p className="price">
        <strong>{price(plan.monthly_price_cents)}</strong>
        {plan.monthly_price_cents > 0 && <span> / month</span>}
      </p>
      <ul>
        <li>{plan.max_monitors} website monitors</li>
        <li>Checks every {mins} minute{mins === 1 ? '' : 's'}</li>
        <li>{plan.history_retention_days} days of history</li>
        {(plan.max_regions ?? 1) > 1 && <li>Checks from up to {plan.max_regions} locations</li>}
        <li>Website and API monitoring</li>
        <li>SSL certificate expiry warnings</li>
        <li>Email, Slack and webhook alerts</li>
        <li>Public status page</li>
        {plan.maintenance_enabled && <li>Maintenance windows</li>}
      </ul>
      {plan.monthly_price_cents === 0 ? (
        <Link className="button primary" to="/register">Start free</Link>
      ) : billingEnabled ? (
        <Link className="button primary" to={signedIn ? '/billing' : '/register'}>{signedIn ? `Choose ${plan.name}` : `Start free, upgrade to ${plan.name}`}</Link>
      ) : (
        <button disabled>Paid plans open soon</button>
      )}
    </div>
  )
  // The featured plan gets daisyUI's gold aura instead of a solid border.
  return <li className="plan-slot" data-reveal>{featured ? <div className="d-aura d-aura-gold plan-aura">{card}</div> : card}</li>
}

export function LandingPage() {
  const { status } = useAuth()
  const [plans, setPlans] = useState<Plan[] | null>(null)
  const multiLocation = plans?.some((p) => (p.max_regions ?? 1) > 1) ?? false
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

  const rootRef = useRef<HTMLDivElement>(null)
  const heroRef = useRef<HTMLElement>(null)
  const artRef = useRef<HTMLImageElement>(null)
  const scrambleRef = useRef<HTMLSpanElement>(null)

  useEffect(() => {
    if (prefersReducedMotion() || !rootRef.current) return
    return inScope(rootRef.current, (add) => {
      if (scrambleRef.current) add(rotateScramble(scrambleRef.current, PHRASES))
      if (artRef.current && heroRef.current) parallax(artRef.current, heroRef.current)
    })
  }, [])

  return (
    <div className="landing" data-theme="light" ref={rootRef}>
      <header className="landing-header">
        <img className="landing-logo" src={brand.logoWhite} alt="HawkEye" width={220} height={43} />
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

      <section className="d-hero" ref={heroRef}>
        <div className="d-hero-content hero">
        <div className="hero-title">
          <p className="eyebrow">Website monitoring, without the guesswork</p>
          <h1>Your websites. Always in sight.</h1>
        </div>
        <div className="hero-text">
          <p className="scramble-line">
            <span className="sr-only">HawkEye watches your websites, alerts you to incidents, tracks your uptime and measures response times.</span>
            <span aria-hidden="true">
              HawkEye <span className="scramble" ref={scrambleRef}>{PHRASES[0]}</span>
            </span>
          </p>
          <p>
            Know when your websites go down, spot changes in response times, and keep every monitor in view from one simple
            dashboard.
          </p>
          <div className="row">
            <Link className="button primary large" to={signedIn ? '/dashboard' : '/register'}>
              {signedIn ? 'Open dashboard' : 'Start monitoring free'}
            </Link>
            <span className="hint">No credit card required · Set up in minutes</span>
          </div>
          <ul className="value-strip">
            <li className="d-badge d-badge-soft d-badge-success">Uptime tracking</li>
            <li className="d-badge d-badge-soft d-badge-error">Outage alerts</li>
            <li className="d-badge d-badge-soft d-badge-warning">SSL expiry warnings</li>
            <li className="d-badge d-badge-soft d-badge-info">Performance history</li>
            <li className="d-badge d-badge-soft d-badge-accent">Public status page</li>
          </ul>
        </div>
        <img
          ref={artRef}
          className="hero-art"
          src={brand.dashboardLaptop}
          alt="Illustration of a HawkEye dashboard listing monitored websites as online, with an uptime ring and a response-time chart."
          width={566}
          height={463}
        />
        </div>
      </section>

      <section aria-labelledby="problem-h" className="section">
        <h2 id="problem-h">Your website doesn’t clock out. Neither should your monitoring.</h2>
        <p>
          A website can fail at midnight, slow to a crawl during a busy afternoon, or return an error while everything else appears
          normal. You shouldn’t have to keep refreshing a browser to find out whether it’s working. HawkEye checks your websites on a
          schedule, records their responses, and alerts you when repeated failures indicate a potential outage.
        </p>
        <Reveal as="ul" className="features">
          {PROBLEMS.map((f) => (
            <li key={f.title} className="feature" data-reveal>
              <h3>{f.title}</h3>
              <p>{f.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section aria-labelledby="features-h" className="section">
        <h2 id="features-h">Everything you need to keep watch.</h2>
        <p>Know what’s working, see what isn’t, and understand what happened.</p>
        <Reveal as="ul" className="features">
          {[...FEATURES, ...(multiLocation ? [LOCATION_FEATURE] : [])].map((f) => (
            <li key={f.title} className="feature" data-reveal>
              <img src={f.icon} alt="" width={64} height={64} />
              <h3>{f.title}</h3>
              <p><strong>{f.lead}</strong></p>
              <p>{f.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section className="section split">
        <img src={brand.icons.websites} alt="" width={170} height={170} />
        <div>
          <h2>Checks that tell the truth</h2>
          <p>
            A failed check is not an incident. A site counts as down only after consecutive failed checks, and as recovered only after
            consecutive successful ones. You get one alert when an outage is confirmed and one when it ends.
          </p>
        </div>
        <img src={brand.icons.notifications} alt="" width={150} height={150} />
      </section>

      <section aria-labelledby="audience-h" className="section">
        <h2 id="audience-h">Built for the people who keep the web running</h2>
        <Reveal as="ul" className="features audiences">
          {AUDIENCES.map((f) => (
            <li key={f.title} className="feature" data-reveal>
              <h3>{f.title}</h3>
              <p><strong>{f.lead}</strong></p>
              <p>{f.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section aria-labelledby="how-h" className="section">
        <h2 id="how-h">Up and running in minutes.</h2>
        <Reveal as="ol" className="steps">
          {STEPS.map((f, i) => (
            <li key={f.title} className="feature" data-reveal>
              <h3><span className="step-no d-badge d-badge-primary">{String(i + 1).padStart(2, '0')}</span> {f.title}</h3>
              <p>{f.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section aria-labelledby="pricing-h" className="section">
        <h2 id="pricing-h">Simple plans. Clear visibility.</h2>
        <p className="hint">Start free. Upgrade when your monitoring needs grow. Every plan includes website and API monitoring, SSL certificate expiry warnings, incident tracking, email, Slack and webhook alerts, a public status page, and uptime reports. Paid plans add maintenance windows. Plans otherwise differ in how many monitors you can have, how often they are checked, and how long history is kept. The free plan is a real plan, not a trial, and monitors are never deleted if you move to a smaller plan.</p>
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

      <section className="section split trust">
        <img src={brand.icons.shield} alt="" width={170} height={170} />
        <div>
          <h2>Built to be safe</h2>
          <p>
            Passwords are hashed with Argon2id, sessions are protected against cross-site attacks, each organization’s data is kept
            separate, and checks cannot be used to probe private networks.
          </p>
        </div>
      </section>

      <section aria-labelledby="faq-h" className="section">
        <h2 id="faq-h" className="centered">Frequently asked questions</h2>
        <Reveal className="faq">
          {[...FAQ, ...(multiLocation ? [LOCATION_FAQ] : [])].map((f) => (
            <details key={f.q} className="d-collapse d-collapse-arrow border border-base-300 bg-base-100" data-reveal>
              <summary className="d-collapse-title font-semibold">{f.q}</summary>
              <div className="d-collapse-content">
                <p>{f.a}</p>
              </div>
            </details>
          ))}
        </Reveal>
      </section>

      <footer className="landing-footer d-footer">
        <aside>
          <img src={brand.logoWhite} alt="HawkEye" width={160} height={32} />
          <p>
            Your websites. Always in sight.
            <br />
            Website monitoring for people who need to know when something changes.
          </p>
        </aside>
        <nav aria-label="Product">
          <h6 className="d-footer-title">Product</h6>
          <a className="d-link d-link-hover" href="#features-h">Features</a>
          <a className="d-link d-link-hover" href="#pricing-h">Pricing</a>
          {signedIn ? (
            <Link className="d-link d-link-hover" to="/dashboard">Dashboard</Link>
          ) : (
            <>
              <Link className="d-link d-link-hover" to="/register">Sign up</Link>
              <Link className="d-link d-link-hover" to="/login">Log in</Link>
            </>
          )}
        </nav>
        <nav aria-label="Company">
          <h6 className="d-footer-title">Company</h6>
          <Link className="d-link d-link-hover" to="/contact">Contact us</Link>
          <Link className="d-link d-link-hover" to="/privacy">Privacy policy</Link>
          <Link className="d-link d-link-hover" to="/terms">Terms of service</Link>
        </nav>
        <nav aria-label="Social media">
          <h6 className="d-footer-title">Follow</h6>
          <div className="social">
            {SOCIALS.map((s) => (
              <a key={s.name} href={s.href} target="_blank" rel="noopener noreferrer" className="d-btn d-btn-circle d-btn-ghost" aria-label={`HawkEye on ${s.name}`}>
                <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"><path d={s.path} /></svg>
              </a>
            ))}
          </div>
        </nav>
      </footer>
      <p className="copyright">© {new Date().getFullYear()} HawkEye. All rights reserved.</p>
    </div>
  )
}
