import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { api, type Plan } from '../api'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { Reveal } from '../components/Reveal'
import { inScope, parallax, prefersReducedMotion, rotateScramble } from '../motion'

// Simple Icons paths (CC0), 24x24.
const SOCIALS = [
  { name: 'X', path: 'M14.234 10.162 22.977 0h-2.072l-7.591 8.824L7.251 0H.258l9.168 13.343L.258 24H2.33l8.016-9.318L16.749 24h6.993zm-2.837 3.299-.929-1.329L3.076 1.56h3.182l5.965 8.532.929 1.329 7.754 11.09h-3.182z' },
  { name: 'LinkedIn', path: 'M20.447 20.452h-3.554v-5.569c0-1.328-.027-3.037-1.852-3.037-1.853 0-2.136 1.445-2.136 2.939v5.667H9.351V9h3.414v1.561h.046c.477-.9 1.637-1.85 3.37-1.85 3.601 0 4.267 2.37 4.267 5.455v6.286zM5.337 7.433a2.062 2.062 0 0 1-2.063-2.065 2.064 2.064 0 1 1 2.063 2.065zm1.782 13.019H3.555V9h3.564v11.452zM22.225 0H1.771C.792 0 0 .774 0 1.729v20.542C0 23.227.792 24 1.771 24h20.451C23.2 24 24 23.227 24 22.271V1.729C24 .774 23.2 0 22.222 0h.003z' },
  { name: 'GitHub', path: 'M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12' },
  { name: 'YouTube', path: 'M23.498 6.186a3.016 3.016 0 0 0-2.122-2.136C19.505 3.545 12 3.545 12 3.545s-7.505 0-9.377.505A3.017 3.017 0 0 0 .502 6.186C0 8.07 0 12 0 12s0 3.93.502 5.814a3.016 3.016 0 0 0 2.122 2.136c1.871.505 9.376.505 9.376.505s7.505 0 9.377-.505a3.015 3.015 0 0 0 2.122-2.136C24 15.93 24 12 24 12s0-3.93-.502-5.814zM9.545 15.568V8.432L15.818 12l-6.273 3.568z' },
]

const PHRASES = ['watches your websites.', 'alerts you to incidents.', 'tracks your uptime.', 'measures response times.']

const PROBLEMS = [
  {
    title: 'The outage you didn’t see coming',
    text: 'A website goes offline and you find out from a customer. HawkEye helps shorten that blind spot by checking availability and notifying you when an outage is confirmed.',
  },
  {
    title: 'The performance problem hiding in plain sight',
    text: 'A slow response can be an early warning sign. Track response times over time and spot changes that deserve a closer look.',
  },
  {
    title: 'The growing list of websites',
    text: 'Managing one website is straightforward. Managing ten, fifty, or a hundred is different. Bring your websites together in one dashboard and see which ones need attention.',
  },
]

const FEATURES = [
  {
    icon: brand.icons.websites,
    title: 'Website and API monitoring',
    lead: 'Know when a website or API stops responding.',
    text: 'HawkEye checks your websites and APIs at regular intervals and records whether they respond successfully. Send custom requests with headers, a body and authentication, and verify what comes back, including values in a JSON response. When repeated failures meet the alert threshold, an incident is created and your notifications are triggered.',
  },
  {
    icon: brand.icons.analytics,
    title: 'Response-time tracking',
    lead: 'Spot slowdowns before they become bigger problems.',
    text: 'A website can be online and still deliver a frustrating experience. Track response times, review historical trends, and investigate when a website becomes slower than usual.',
  },
  {
    icon: brand.icons.history,
    title: 'Incident tracking',
    lead: 'Every incident. Clearly accounted for.',
    text: 'See when a problem began and when service recovered. Keep an organized history of incidents instead of piecing together events from messages and browser tabs.',
  },
  {
    icon: brand.icons.email,
    title: 'Alerts where you work',
    lead: 'The right signal. When it matters.',
    text: 'HawkEye alerts you when an outage is confirmed and when the website recovers: one message each, not a flood. Send alerts by email, to a Slack channel, or to your own systems as signed webhooks. Email recipients must confirm their address, and Slack and webhook destinations are sent a test message before they are saved.',
  },
  {
    icon: brand.icons.websites,
    title: 'Public status page',
    lead: 'Tell your customers before they ask.',
    text: 'Publish a status page at an address you choose, showing whether your services are up, their daily uptime and recent incidents. You decide which monitors appear and what they are called. Web addresses and error details are never shown.',
  },
  {
    icon: brand.icons.history,
    title: 'Maintenance windows',
    lead: 'Planned work shouldn’t page you.',
    text: 'Schedule a window for a deploy or upgrade, once or on a daily or weekly repeat. HawkEye keeps checking, but opens no incident, sends no alert, and leaves those checks out of your uptime. Included in the Pro and Agency plans.',
  },
  {
    icon: brand.icons.shield,
    title: 'SSL certificate alerts',
    lead: 'Never be surprised by an expired certificate.',
    text: 'HawkEye reads the SSL certificate of every HTTPS monitor and shows a live countdown to its expiry. You get an email 30, 14, 7 and 1 days before it expires, and again if it does, so there is time to renew.',
  },
  {
    icon: brand.icons.settings,
    title: 'One dashboard, clear reports',
    lead: 'Every website. One clear picture.',
    text: 'Review availability, response times, recent incidents, expiring certificates and check-based uptime history in one workspace, for the time periods your plan supports.',
  },
]

// Shown only when this installation really offers more than one location (the plans report how many each allows).
const LOCATION_FEATURE = {
  icon: brand.icons.shield,
  title: 'Checks from several locations',
  lead: 'One network hiccup is not an outage.',
  text: 'On paid plans each monitor can be checked from several locations at once. A monitor counts as down only when most of its locations agree, so a problem at a single location never wakes you up, and you can see how each location sees your site.',
}
const LOCATION_FAQ = {
  q: 'Can HawkEye check from more than one location?',
  a: 'Yes, on paid plans. Pick the locations for each monitor; HawkEye checks from all of them and raises an incident only when most of them see a failure, which avoids false alarms from a problem at one location. The monitor page shows how each location sees your site. Free monitors are checked from the primary location.',
}

const AUDIENCES = [
  { title: 'Freelancers', lead: 'Spend less time checking. More time creating.', text: 'You’re responsible for the website, the fixes, the updates and the client relationship. HawkEye adds an automated layer of visibility, so you can identify availability problems without manually checking every project.' },
  { title: 'Web agencies', lead: 'Keep an eye on every client website.', text: 'As your client list grows, so does the work of keeping track of it. Centralize website monitoring, review incidents across projects, and use history to show the ongoing care you provide.' },
  { title: 'Independent business owners', lead: 'Your website is too important to leave unchecked.', text: 'Downtime can interrupt inquiries, sales and customer service. HawkEye helps you know when availability problems arise so you can investigate sooner.' },
  { title: 'Developers and small teams', lead: 'Make availability visible.', text: 'Keep a practical record of HTTP responses, failures and latency trends. Check your APIs too: send authenticated requests and verify the JSON that comes back, without adopting a complex observability stack.' },
]

const STEPS = [
  { title: 'Add your website', text: 'Enter a name and URL. HawkEye validates the monitor configuration and adds it to your dashboard.' },
  { title: 'Let HawkEye keep watch', text: 'Your website is checked at your plan’s monitoring interval. Results are recorded so you can review status and response-time history.' },
  { title: 'Get notified when something changes', text: 'When failures meet the detection threshold, HawkEye opens an incident and alerts your destinations: email, Slack or a webhook.' },
  { title: 'Investigate and move forward', text: 'Review the incident, inspect the recorded results and take action. When successful checks confirm recovery, HawkEye resolves the incident and tells you.' },
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
  return (
    <li className={`plan${featured ? ' featured' : ''}`} data-reveal>
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
    </li>
  )
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

      <section className="d-hero" ref={heroRef}>
        <div className="d-hero-content hero">
        <div className="hero-text">
          <p className="eyebrow">Website monitoring, without the guesswork</p>
          <h1>Your websites. Always in sight.</h1>
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
            <li className="d-badge d-badge-soft d-badge-neutral">Public status page</li>
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
        <img src={brand.websiteMagnifier} alt="" width={180} height={196} />
        <div>
          <h2>Checks that tell the truth</h2>
          <p>
            A failed check is not an incident. A site counts as down only after consecutive failed checks, and as recovered only after
            consecutive successful ones. You get one alert when an outage is confirmed and one when it ends.
          </p>
        </div>
        <img src={brand.alertBell} alt="" width={140} height={152} />
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
        <img src={brand.cloudSecurity} alt="" width={232} height={192} />
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
          <img src={brand.logo} alt="HawkEye" width={120} height={43} />
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
              // Placeholders: accounts do not exist yet, so these are disabled buttons, not links.
              <button key={s.name} type="button" className="d-btn d-btn-circle d-btn-ghost" disabled title={`${s.name}: coming soon`} aria-label={`${s.name} (coming soon)`}>
                <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"><path d={s.path} /></svg>
              </button>
            ))}
          </div>
        </nav>
      </footer>
      <p className="copyright">© {new Date().getFullYear()} HawkEye. All rights reserved.</p>
    </div>
  )
}
