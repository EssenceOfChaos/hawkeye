import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { type BillingInterval, type Plan } from '../api'
import { usePlans } from '../hooks'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { IntervalToggle, PlanDetails } from '../components/PlanDetails'
import { ComingSoon } from '../components/ComingSoon'
import { MonitoringFlow } from '../components/MonitoringFlow'
import { ProductShots } from '../components/ProductShots'
import { Reveal } from '../components/Reveal'
import { SiteFooter } from '../components/SiteFooter'
import { AnimatedScout } from '../components/AnimatedScout'
import { AgentTerminals } from '../components/AgentTerminals'
import { inScope, parallax, prefersReducedMotion, rotateScramble } from '../motion'

const PHRASES = ['watches your websites.', 'alerts you to incidents.', 'tracks your uptime.', 'measures response times.']

const FEATURES = [
  {
    icon: brand.icons.websites,
    title: 'Website and API monitoring',
    lead: 'Know when a website or API stops responding.',
    text: 'Send custom requests, authenticate, and verify what comes back. Import your OpenAPI spec or Postman collection, or paste a cURL command, instead of filling in forms. Check whole flows too, such as signing in and then fetching an account.',
  },
  {
    icon: brand.icons.cron,
    title: 'Cron job monitoring',
    lead: 'Know when a scheduled job stops running.',
    text: 'Backups, reports and queue workers ping HawkEye when they run. Miss one, or report a failure, and you hear about it.',
  },
  {
    icon: brand.icons.shield,
    title: 'DNS monitoring',
    lead: 'Know when a DNS record changes or disappears.',
    text: 'Watch address, mail, name server and TXT records, and hear about it when one changes, without typing in what it should be.',
  },
  {
    icon: brand.icons.responseTime,
    title: 'Response-time tracking',
    lead: 'Spot slowdowns early.',
    text: 'Track response times over time, and see where the time goes: DNS, connecting, TLS or your server.',
  },
  {
    icon: brand.icons.incident,
    title: 'Incident tracking',
    lead: 'Every incident, clearly accounted for.',
    text: 'See when a problem began and when service recovered.',
  },
  {
    icon: brand.icons.notifications,
    title: 'Alerts where you work',
    lead: 'The right signal. When it matters.',
    text: 'One alert when an outage is confirmed, one when it ends. Email, Slack, Teams, Discord, Telegram, PagerDuty or webhook.',
  },
  {
    icon: brand.icons.statusPage,
    title: 'Public status pages',
    lead: 'Tell your customers before they ask.',
    text: 'Share live status, uptime and incidents, and post updates while you fix things. One page per client on paid plans, on the client’s own domain with Agency. You choose what appears.',
  },
  {
    icon: brand.icons.pdfReports,
    title: 'Monthly PDF reports',
    lead: 'Show clients how their site performed.',
    text: 'A clear uptime report for each website: downtime, incidents and a day-by-day chart. Pro and Agency. Agency adds your own name and logo.',
  },
  {
    icon: brand.icons.maintenance,
    title: 'Maintenance windows',
    lead: 'Planned work shouldn’t page you.',
    text: 'Schedule quiet periods, or let your CI pipeline quiet alerts while it deploys. Included in Pro and Agency.',
  },
  {
    icon: brand.icons.ssl,
    title: 'SSL certificate alerts',
    lead: 'Never be surprised by an expired certificate.',
    text: 'A live countdown, plus warnings 30, 14, 7 and 1 days before expiry.',
  },
  {
    icon: brand.icons.history,
    title: 'Domain expiry alerts',
    lead: 'Don’t lose your domain to a missed renewal.',
    text: 'Warnings 30, 14, 7 and 1 days before a domain registration expires.',
  },
  {
    icon: brand.icons.dashboard,
    title: 'One dashboard, clear reports',
    lead: 'Every website. One clear picture.',
    text: 'Availability, response times, incidents, certificates and domains in one place.',
  },
  {
    icon: brand.icons.developers,
    title: 'Works with Claude',
    lead: 'Ask “what’s down right now?”',
    text: 'Add HawkEye from Claude’s connector directory to check uptime and incidents, pause monitors and post updates by asking. Claude asks before anything public.',
  },
]

// Shown only when this installation really offers more than one location (the plans report how many each allows).
const LOCATION_FEATURE = {
  icon: brand.icons.locations,
  title: 'Checks from several locations',
  lead: 'One network hiccup is not an outage.',
  text: 'On paid plans, a monitor is offline only when most locations agree.',
}
const LOCATION_FAQ = {
  q: 'Can HawkEye check from more than one location?',
  a: 'Yes, on paid plans. Pick the locations for each monitor; HawkEye checks from all of them and raises an incident only when most of them see a failure, which avoids false alarms from a problem at one location. The monitor page shows how each location sees your site. Free monitors are checked from the primary location.',
}

const AUDIENCES = [
  { icon: brand.audience.freelancer, title: 'Freelancers', lead: 'Spend less time checking. More time creating.', text: 'Know when a client site is offline without refreshing every project.' },
  { icon: brand.audience.agency, title: 'Web agencies', lead: 'Keep an eye on every client website.', text: 'Monitor every client in one place and show the care you provide.' },
  { icon: brand.audience.business, title: 'Independent business owners', lead: 'Your website is too important to leave unchecked.', text: 'Hear about downtime before your customers do.' },
  { icon: brand.audience.developer, title: 'Developers and small teams', lead: 'Make availability visible.', text: 'Check your APIs, cron jobs and DNS records too: authenticated requests, JSON checks, heartbeats and record lookups, no complex stack.' },
]

// "Set up from your domain" (internal/discovery, SetupPage: the useful suggestions are preselected, one button adds them) and the OpenAPI/Postman
// import (OpenApiImportPage). Keep this in step with what those pages do.
const STEPS = [
  { title: 'Enter your domain', text: 'HawkEye suggests monitors for your website, APIs, certificate, domain and mail records. Add them in one click.' },
  { title: 'Or import your API', text: 'Upload an OpenAPI, Swagger or Postman file and get a check for every endpoint.' },
  { title: 'Get notified when something changes', text: 'Confirmed failures open an incident and alert you by email, chat, PagerDuty or webhook.' },
  { title: 'Investigate and move forward', text: 'Review the incident. HawkEye resolves it when checks recover.' },
]

const FAQ = [
  { q: 'How does website monitoring work?', a: 'You add a website URL, or enter your domain and let HawkEye suggest monitors for your home page, health endpoint, API and email records, then pick the ones you want. HawkEye periodically sends an HTTP request, records the result, and evaluates whether repeated failures indicate a potential outage.' },
  { q: 'Can I sign in with Google, GitHub or X?', a: 'Yes. Choose Continue with Google, GitHub or X to create an account or sign in, or use your email address and a password as before. One account can use any of them, and the sign-in page marks the one you used last. If you turned on two-factor sign-in, HawkEye still asks for your code.' },
  { q: 'Do I need a credit card?', a: 'No. The free plan does not ask for one. A payment method is only needed if you choose a paid plan.' },
  { q: 'What happens when my website goes offline?', a: 'When repeated failures meet the incident threshold, HawkEye creates an incident and sends an alert to your email, chat (Slack, Microsoft Teams, Discord or Telegram), PagerDuty or webhook destinations. It keeps checking, and tells you when the website recovers.' },
  { q: 'If one server hosting twenty sites goes down, do I get twenty alerts?', a: 'No. The first alert goes out at once, and the others that follow within two minutes arrive as one message listing them, sent separately to each destination, so a client’s channel only lists that client’s sites. A site that keeps going down and coming back (three outages in an hour) gets one “flapping” alert instead of one per outage, and you are alerted straight away if it then stays down. Both are on by default and can be turned off.' },
  { q: 'Will HawkEye guarantee that my website is always available?', a: 'No monitoring service can guarantee that a website will always be available or detect every possible problem. HawkEye provides periodic checks and recorded results to help you identify and investigate availability issues.' },
  { q: 'Does HawkEye warn me before my SSL certificate expires?', a: 'Yes, on every plan. For each HTTPS monitor, HawkEye reads the certificate your site presents during its checks, shows the expiry date and a countdown, and alerts your destinations 30, 14, 7 and 1 days before it expires and again if it does. Renew the certificate and HawkEye picks up the new date automatically. It reports the certificate it sees for the monitored address; it cannot tell you about other hostnames or certificates revoked early.' },
  { q: 'Does HawkEye warn me before my domain name expires?', a: 'Yes, on every plan, up to the number of domains your plan lists. HawkEye works out the registered domain of each website you monitor. It looks up the registration expiry date with the domain’s registry once a day and alerts your destinations 30, 14, 7 and 1 days before it expires. Not every registry shares this date, and some hide it for privacy. For those domains HawkEye shows “Expiry unavailable”. Warnings are a reminder; you remain responsible for renewing.' },
  { q: 'Where can HawkEye send alerts?', a: 'To email addresses, to Slack, Microsoft Teams, Discord and Telegram channels, to PagerDuty (an outage opens a PagerDuty incident and its recovery resolves it), and to any HTTPS endpoint as a signed JSON webhook so your own systems can react. Every alert is queued and retried if delivery fails. Slack and webhook destinations are tested when you add them and available on every plan. SMS is not available yet.' },
  { q: 'Can I show my customers a status page?', a: 'Yes, on every plan: one page on Free, and on paid plans several, so you can give each client or product its own. Choose an address, the monitors to show and the names to show them under, then publish. The page shows each service’s current state, daily uptime and recent incidents, and during an incident you can post updates such as “Investigating” or “Fixed” for your visitors to read. On Pro and Agency, HawkEye can write a first draft of each update for you to edit, if you turn that on. It never shows your monitored web addresses or error details, and it stays private until you publish it. On Pro and Agency you can add a logo, an accent color and a link back to your client’s website, and Agency can remove “Powered by HawkEye” and serve each page at an address on your client’s own domain, such as status.yourclient.com, with a certificate HawkEye looks after. Daily history follows your plan: 7 days on Free, up to 90 on paid plans.' },
  { q: 'Can I show my uptime on my website or GitHub README?', a: 'Yes, on every plan. Open a monitor, turn on its public uptime badge and copy the code: HTML for a website (paste it into your site’s HTML, or into a “Custom HTML” or “Embed” block in WordPress, Squarespace, Wix, Webflow or Shopify) or Markdown for a README on GitHub, GitLab or anywhere else that shows Markdown. There are two sizes: a website badge and a smaller README badge. The badge shows the uptime percentage over the last 30 days (7 on Free) and whether the site is up right now, updates itself every few minutes, and never shows the monitor’s name or address. Turning it off removes it everywhere it is embedded.' },
  { q: 'How do I avoid false alerts during planned maintenance?', a: 'Schedule a maintenance window on a Pro or Agency plan. For all or some of your monitors, once or repeating daily or weekly, HawkEye keeps checking but does not open incidents or send alerts, and leaves those checks out of uptime. An incident that is already open stays open until the monitor recovers. For deploys, two lines in your CI pipeline quiet alerts while it deploys and check your sites afterwards.' },
  { q: 'Can HawkEye tell when my page loads but shows an error?', a: 'Yes, on every plan. Some failures still answer “200 OK”: a WordPress database error, a hosting “Account Suspended” page, a server’s default page, or an expired domain that has been parked. In a monitor’s “Page content” section, add text the page must contain (such as “Add to cart” or your company name) and text it must not contain, or pick a common problem from the list with one click. If the check fails you are alerted just as for an outage.' },
  { q: 'Can I monitor an API?', a: 'Yes. Choose the request method, add custom headers and a request body, and authenticate with Basic, a bearer token or an API-key header, or paste a cURL command and HawkEye fills all of that in. Have an OpenAPI or Swagger file, or a Postman collection? Import it, and HawkEye lists a check for each endpoint, checking the status code (and, from an OpenAPI spec, the fields every response has); you choose which to add. You can then check the status code, look for text in the response, or verify a value in a JSON response, including number comparisons such as a queue size of at most 500. Send a test request to see the response and click a field to check it. If your API uses distributed tracing, each check can send a trace context header, so it shows up in your tracing tool with an ID you can look up when a check fails. A multi-step API check runs several requests in a row, such as signing in and then fetching an account with the token it returned, and tells you which step failed. Passwords, tokens and API keys are stored encrypted and are never shown again. Methods that change data (POST, PUT, PATCH and DELETE) are repeated on every check, so they require a verified email address and run at most every 5 minutes.' },
  { q: 'Can HawkEye monitor cron jobs and scheduled tasks?', a: 'Yes, on every plan. Create a heartbeat monitor and HawkEye gives it a private ping address. Add one line to the end of your cron job, backup script, scheduled task or queue worker so it requests that address each time it runs. If a ping does not arrive on schedule (you choose how often to expect one and how much grace time to allow), or the job reports a failure or a non-zero exit status, HawkEye opens an incident and alerts you like it does for a website that is down. Heartbeat monitors count toward your plan’s monitor limit. See the heartbeat monitoring guide in our docs for examples.' },
  { q: 'Can HawkEye monitor my DNS records?', a: 'Yes, on every plan. Create a DNS monitor for a host name and choose an A, AAAA, MX, NS or TXT record. HawkEye looks it up on a schedule and alerts you when the record disappears, when it changes (showing before and after, without you entering a value: a new mail server, name servers that aren’t yours, an edited SPF or DMARC record), and, if you enter the value you expect, when it stops matching. Each DNS monitor counts toward your plan’s monitor limit.' },
  { q: 'Does HawkEye have an API?', a: 'Yes. Create an API key in your account and use it from your own scripts, dashboards and tools to read your monitors, uptime and incidents, on every plan. A read and write key can also add monitors on every plan, and on Pro and Agency make other changes, such as pausing monitors during a deploy. Each key is read-only or read and write, expires after 90 days unless you choose another date (or no expiry), and can be revoked at any time. Account, billing and team settings always need you to sign in, so a key cannot be used to take over your account. See the API guide in our docs.' },
  { q: 'Can I use HawkEye with Claude or another AI assistant?', a: 'Yes. HawkEye is listed in Claude’s connector directory: add it in Claude, sign in to HawkEye and choose what Claude may do. Claude Code and Codex have HawkEye plugins, and other MCP clients can connect to HawkEye’s MCP server with an API key. Either way, the assistant can answer questions such as “what’s down right now?” or “how was uptime this month?”, and suggest monitors for your site. With read and write access it can also add monitors on every plan, and on Pro or Agency pause monitors, post incident updates and schedule maintenance; Claude asks you before posting anything public, and never sees the passwords a check uses. The assistant acts as you, with your permissions, and can never reach your password, billing or team settings. See the Claude and Codex guides in our docs.' },
  { q: 'Does HawkEye support two-factor sign-in?', a: 'Yes, on every plan. Turn it on in your account settings and HawkEye asks for a code from an authenticator app (such as 1Password, Google Authenticator or Authy) after your password. You get recovery codes in case you lose your phone. On a team, the owner can require it for everyone; members who don’t use it yet are emailed and have 7 days to set it up.' },
  { q: 'What happens if I downgrade?', a: 'Your existing monitors are never deleted automatically. You may not be able to add new ones until you are within the new plan’s limits, and history older than the new plan’s retention period is removed. Maintenance windows you already created keep working, but you cannot create new ones until you are on a paid plan. Domains beyond the new plan’s limit stay in your list but are no longer looked up. API keys keep reading, but stop making changes until you upgrade again.' },
]

export function PlanCard({ plan, featured, billingEnabled, signedIn, interval = 'month' }: {
  plan: Plan; featured: boolean; billingEnabled: boolean; signedIn: boolean; interval?: BillingInterval
}) {
  const card = (
    <div className={`plan${featured ? ' featured' : ''}`}>
      <h3>{plan.name}</h3>
      <PlanDetails plan={plan} interval={interval} />
      {plan.monthly_price_cents === 0 ? (
        <Link className="button primary" to="/register">Create free account</Link>
      ) : billingEnabled ? (
        <Link className="button primary" to={signedIn ? (interval === 'year' ? '/billing?interval=year' : '/billing') : '/register'}>{signedIn ? `Choose ${plan.name}` : `Sign up, then choose ${plan.name}`}</Link>
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
  const { plans, billingEnabled, annualEnabled } = usePlans()
  const [period, setPeriod] = useState<BillingInterval>('month')
  const multiLocation = plans?.some((p) => (p.max_regions ?? 1) > 1) ?? false
  const multiSeat = plans?.some((p) => (p.max_team_members ?? 1) > 1) ?? false
  const subMinute = plans?.some((p) => p.minimum_check_interval_seconds < 60) ?? false
  // What paid plans add over Free, in words with no numbers (the cards carry those). Keep it in step with PlanDetails when a plan feature is added.
  const paidAdvantages =
    `Paid plans add more monitors, faster checks, longer history, a higher domain limit, ${multiLocation ? 'checks from several locations, ' : ''}maintenance windows, more multi-step API checks, monthly PDF uptime reports, AI-drafted incident updates and API keys that can make every change. ` +
    `Agency ${multiSeat ? 'also ' : ''}adds ${subMinute ? 'the fastest checks, ' : ''}${multiSeat ? 'team members, ' : ''}reports with your own name and logo, and status pages on your clients’ own domains.`

  const signedIn = status === 'authenticated'

  const rootRef = useRef<HTMLDivElement>(null)
  const heroRef = useRef<HTMLElement>(null)
  const artRef = useRef<HTMLDivElement>(null)
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
            <Link to="/login">Sign in</Link>
            <Link className="button primary" to="/register">Sign up free</Link>
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
            Know when your websites go offline, spot changes in response times, and keep every monitor in view from one simple
            dashboard.
          </p>
          <div className="row">
            <Link className="button primary large" to={signedIn ? '/dashboard' : '/register'}>
              {signedIn ? 'Open dashboard' : 'Create free account'}
            </Link>
            <span className="hint">No credit card required · Set up in minutes</span>
          </div>
          <ul className="value-strip">
            <li className="d-badge d-badge-soft d-badge-success">Uptime tracking</li>
            <li className="d-badge d-badge-soft d-badge-error">Outage alerts</li>
            <li className="d-badge d-badge-soft d-badge-neutral">Outages confirmed before alerts</li>
            <li className="d-badge d-badge-soft d-badge-warning">SSL expiry warnings</li>
            <li className="d-badge d-badge-soft d-badge-info">Performance history</li>
            <li className="d-badge d-badge-soft d-badge-accent">Public status page</li>
            <li className="d-badge d-badge-soft d-badge-primary">Cron job and DNS monitoring</li>
            <li className="d-badge d-badge-soft d-badge-secondary">Multi-step API checks</li>
          </ul>
        </div>
        <div className="hero-art-wrap" ref={artRef}>
          <img
            className="hero-art"
            src={brand.dashboardLaptop}
            alt="Illustration of a HawkEye dashboard listing monitored websites as online, with an uptime ring and a response-time chart."
            width={566}
            height={463}
          />
          <AnimatedScout size={200} className="hero-scout" />
        </div>
        </div>
      </section>

      <section aria-labelledby="how-h" className="section how-it-works">
        <p className="eyebrow">How it works</p>
        <h2 id="how-h">From a failed check to the right people.</h2>
        <p>
          When a check fails, HawkEye checks again 15 seconds later before raising the alarm, so a single blip never wakes anyone and a real outage is
          confirmed in seconds. Then it alerts you by email, chat or webhook, and tells you again when it’s over.
        </p>
        <MonitoringFlow />
      </section>

      <ProductShots />

      <AgentTerminals />

      <section className="section split" aria-labelledby="truth-h">
        <img src={brand.icons.websites} alt="" width={170} height={170} />
        <div>
          <p className="eyebrow">Smart alerts reduce noise</p>
          <h2 id="truth-h">A failed check is not an incident.</h2>
          <p>
            A failed check is checked again 15 seconds later, and a site counts as offline only when that fails too; it counts as recovered only after
            consecutive successful checks. You get one alert when an outage is confirmed and one when it ends.
          </p>
          <p>
            HawkEye verifies its own connection before reporting an outage. If other trusted websites are also unreachable, it disregards the failed check to help prevent false alarms.
          </p>
        </div>
        <img src={brand.icons.notifications} alt="" width={150} height={150} />
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


      <section aria-labelledby="audience-h" className="section">
        <h2 id="audience-h">Built for the people who keep the web running</h2>
        <Reveal as="ul" className="features audiences">
          {AUDIENCES.map((f) => (
            <li key={f.title} className="feature" data-reveal>
              <div className="audience-head">
                <img src={f.icon} alt="" width={64} height={64} />
                <h3>{f.title}</h3>
              </div>
              <p><strong>{f.lead}</strong></p>
              <p>{f.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <section aria-labelledby="setup-h" className="section">
        <h2 id="setup-h">Up and running in a few clicks.</h2>
        <p className="hint">Point HawkEye at your domain or API spec. It sets up the monitoring for you.</p>
        <Reveal as="ol" className="steps">
          {STEPS.map((f, i) => (
            <li key={f.title} className="feature" data-reveal>
              <h3><span className="step-no d-badge d-badge-primary">{String(i + 1).padStart(2, '0')}</span> {f.title}</h3>
              <p>{f.text}</p>
            </li>
          ))}
        </Reveal>
      </section>

      <ComingSoon />

      <section aria-labelledby="pricing-h" className="section">
        <h2 id="pricing-h">Simple plans. Clear visibility.</h2>
        <p className="hint">
          Use HawkEye free for as long as you like. Upgrade when your monitoring needs grow. Every plan includes website and API monitoring (with a multi-step API check), SSL certificate expiry warnings, domain expiry warnings (up to a limit that grows with the plan), incident tracking, alerts by email, Slack, Microsoft Teams, Discord, Telegram, PagerDuty and webhook, and public status pages (one on Free, more on paid plans). {paidAdvantages} The free plan is a real plan, not a trial, and monitors are never deleted if you move to a smaller plan.
        </p>
        {plans === null && (
          <div role="status" aria-label="Loading plans" className="plans-loading">
            <div className="d-skeleton h-56 w-full" />
            <div className="d-skeleton h-56 w-full" />
            <div className="d-skeleton h-56 w-full" />
          </div>
        )}
        {plans?.length === 0 && <p>Pricing is temporarily unavailable. You can still <Link to="/register">create a free account</Link>.</p>}
        {plans && plans.length > 0 && billingEnabled && annualEnabled && <IntervalToggle value={period} onChange={setPeriod} plans={plans} />}
        {plans && plans.length > 0 && (
          <Reveal as="ul" className="plans">
            {plans.map((p) => (
              <PlanCard key={p.code} plan={p} featured={p.code === 'pro'} billingEnabled={billingEnabled} signedIn={signedIn} interval={period} />
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

      <SiteFooter onHome />
    </div>
  )
}
