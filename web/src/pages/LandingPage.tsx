import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { type BillingInterval, type Plan } from '../api'
import { usePlans } from '../hooks'
import { useAuth } from '../auth'
import { brand, STATUS_URL } from '../brand'
import { CookieSettingsButton } from '../components/CookieBanner'
import { IntervalToggle, PlanDetails } from '../components/PlanDetails'
import { ComingSoon } from '../components/ComingSoon'
import { MonitoringFlow } from '../components/MonitoringFlow'
import { ProductShots } from '../components/ProductShots'
import { Reveal } from '../components/Reveal'
import { inScope, parallax, prefersReducedMotion, rotateScramble } from '../motion'

// Simple Icons paths (CC0), 24x24.
const SOCIALS = [
  { name: 'X', href: 'https://x.com/HawkEyeOnX', path: 'M14.234 10.162 22.977 0h-2.072l-7.591 8.824L7.251 0H.258l9.168 13.343L.258 24H2.33l8.016-9.318L16.749 24h6.993zm-2.837 3.299-.929-1.329L3.076 1.56h3.182l5.965 8.532.929 1.329 7.754 11.09h-3.182z' },
  { name: 'Facebook', href: 'https://www.facebook.com/people/HawkEye/61595561066921/', path: 'M9.101 23.691v-7.98H6.627v-3.667h2.474v-1.58c0-4.085 1.848-5.978 5.858-5.978.401 0 .955.042 1.468.103a8.68 8.68 0 0 1 1.141.195v3.325a8.623 8.623 0 0 0-.653-.036 26.805 26.805 0 0 0-.733-.009c-.707 0-1.259.096-1.675.309a1.686 1.686 0 0 0-.679.622c-.258.42-.374.995-.374 1.752v1.297h3.919l-.386 2.103-.287 1.564h-3.246v8.245C19.396 23.238 24 18.179 24 12.044c0-6.627-5.373-12-12-12s-12 5.373-12 12c0 5.628 3.874 10.35 9.101 11.647Z' },
  { name: 'LinkedIn', href: 'https://www.linkedin.com/company/hawkeye-observer/', path: 'M20.447 20.452h-3.554v-5.569c0-1.328-.027-3.037-1.852-3.037-1.853 0-2.136 1.445-2.136 2.939v5.667H9.351V9h3.414v1.561h.046c.477-.9 1.637-1.85 3.37-1.85 3.601 0 4.267 2.37 4.267 5.455v6.286zM5.337 7.433a2.062 2.062 0 0 1-2.063-2.065 2.064 2.064 0 1 1 2.063 2.065zm1.782 13.019H3.555V9h3.564v11.452zM22.225 0H1.771C.792 0 0 .774 0 1.729v20.542C0 23.227.792 24 1.771 24h20.451C23.2 24 24 23.227 24 22.271V1.729C24 .774 23.2 0 22.222 0h.003z' },
  { name: 'Reddit', href: 'https://www.reddit.com/r/HawkEye_Observer/', path: 'M12 0C5.373 0 0 5.373 0 12c0 3.314 1.343 6.314 3.515 8.485l-2.286 2.286C.775 23.225 1.097 24 1.738 24H12c6.627 0 12-5.373 12-12S18.627 0 12 0Zm4.388 3.199c1.104 0 1.999.895 1.999 1.999 0 1.105-.895 2-1.999 2-.946 0-1.739-.657-1.947-1.539v.002c-1.147.162-2.032 1.15-2.032 2.341v.007c1.776.067 3.4.567 4.686 1.363.473-.363 1.064-.58 1.707-.58 1.547 0 2.802 1.254 2.802 2.802 0 1.117-.655 2.081-1.601 2.531-.088 3.256-3.637 5.876-7.997 5.876-4.361 0-7.905-2.617-7.998-5.87-.954-.447-1.614-1.415-1.614-2.538 0-1.548 1.255-2.802 2.803-2.802.645 0 1.239.218 1.712.585 1.275-.79 2.881-1.291 4.64-1.365v-.01c0-1.663 1.263-3.034 2.88-3.207.188-.911.993-1.595 1.959-1.595Zm-8.085 8.376c-.784 0-1.459.78-1.506 1.797-.047 1.016.64 1.429 1.426 1.429.786 0 1.371-.369 1.418-1.385.047-1.017-.553-1.841-1.338-1.841Zm7.406 0c-.786 0-1.385.824-1.338 1.841.047 1.017.634 1.385 1.418 1.385.785 0 1.473-.413 1.426-1.429-.046-1.017-.721-1.797-1.506-1.797Zm-3.703 4.013c-.974 0-1.907.048-2.77.135-.147.015-.241.168-.183.305.483 1.154 1.622 1.964 2.953 1.964 1.33 0 2.47-.81 2.953-1.964.057-.137-.037-.29-.184-.305-.863-.087-1.795-.135-2.769-.135Z' },
  { name: 'Telegram', href: 'https://t.me/hawkeye_community', path: 'M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z' },
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
    text: 'Send custom requests, authenticate, and verify what comes back. Check whole flows too, such as signing in and then fetching an account.',
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
    text: 'Watch address, mail, name server and TXT records, and hear about it when one stops matching what you expect.',
  },
  {
    icon: brand.icons.responseTime,
    title: 'Response-time tracking',
    lead: 'Spot slowdowns early.',
    text: 'Track response times and review trends over time.',
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
    text: 'One alert when an outage is confirmed, one when it ends. Email, Slack, Teams, Discord, Telegram or webhook.',
  },
  {
    icon: brand.icons.statusPage,
    title: 'Public status pages',
    lead: 'Tell your customers before they ask.',
    text: 'Share live status, uptime and incidents, and post updates while you fix things. One page per client on paid plans. You choose what appears.',
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
    text: 'Pause alerts during deploys. Included in Pro and Agency.',
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

const STEPS = [
  { title: 'Add your website', text: 'Enter a name and URL. It joins your dashboard.' },
  { title: 'Let HawkEye keep watch', text: 'Checked at your plan’s interval, with every result recorded.' },
  { title: 'Get notified when something changes', text: 'Confirmed failures open an incident and alert you by email, Slack, Teams, Discord, Telegram or webhook.' },
  { title: 'Investigate and move forward', text: 'Review the incident. When checks recover, HawkEye resolves it and tells you.' },
]

const FAQ = [
  { q: 'How does website monitoring work?', a: 'You add a website URL, or enter your domain and let HawkEye suggest monitors for your home page, health endpoint, API and email records, then pick the ones you want. HawkEye periodically sends an HTTP request, records the result, and evaluates whether repeated failures indicate a potential outage.' },
  { q: 'Do I need a credit card?', a: 'No. The free plan does not ask for one. A payment method is only needed if you choose a paid plan.' },
  { q: 'What happens when my website goes offline?', a: 'When repeated failures meet the incident threshold, HawkEye creates an incident and sends an alert to your email, chat (Slack, Microsoft Teams, Discord or Telegram) or webhook destinations. It keeps checking, and tells you when the website recovers.' },
  { q: 'Will HawkEye guarantee that my website is always available?', a: 'No monitoring service can guarantee that a website will always be available or detect every possible problem. HawkEye provides periodic checks and recorded results to help you identify and investigate availability issues.' },
  { q: 'Does HawkEye warn me before my SSL certificate expires?', a: 'Yes, on every plan. For each HTTPS monitor, HawkEye reads the certificate your site presents during its checks, shows the expiry date and a countdown, and alerts your destinations 30, 14, 7 and 1 days before it expires and again if it does. Renew the certificate and HawkEye picks up the new date automatically. It reports the certificate it sees for the monitored address; it cannot tell you about other hostnames or certificates revoked early.' },
  { q: 'Does HawkEye warn me before my domain name expires?', a: 'Yes, on every plan, up to the number of domains your plan lists. HawkEye works out the registered domain of each website you monitor. It looks up the registration expiry date with the domain’s registry once a day and alerts your destinations 30, 14, 7 and 1 days before it expires. Not every registry shares this date, and some hide it for privacy. For those domains HawkEye shows “Expiry unavailable”. Warnings are a reminder; you remain responsible for renewing.' },
  { q: 'Where can HawkEye send alerts?', a: 'To email addresses, to Slack, Microsoft Teams, Discord and Telegram channels, and to any HTTPS endpoint as a signed JSON webhook so your own systems can react. Every alert is queued and retried if delivery fails. Slack and webhook destinations are tested when you add them and available on every plan. SMS is not available yet.' },
  { q: 'Can I show my customers a status page?', a: 'Yes, on every plan: one page on Free, and on paid plans several, so you can give each client or product its own. Choose an address, the monitors to show and the names to show them under, then publish. The page shows each service’s current state, daily uptime and recent incidents, and during an incident you can post updates such as “Investigating” or “Fixed” for your visitors to read. On Pro and Agency, HawkEye can write a first draft of each update for you to edit, if you turn that on. It never shows your monitored web addresses or error details, and it stays private until you publish it. On Pro and Agency you can add a logo, an accent color and a link back to your client’s website, and Agency can remove “Powered by HawkEye”. Daily history follows your plan: 7 days on Free, up to 90 on paid plans.' },
  { q: 'Can I show my uptime on my website or GitHub README?', a: 'Yes, on every plan. Open a monitor, turn on its public uptime badge and copy the code: HTML for a website (paste it into your site’s HTML, or into a “Custom HTML” or “Embed” block in WordPress, Squarespace, Wix, Webflow or Shopify) or Markdown for a README on GitHub, GitLab or anywhere else that shows Markdown. There are two sizes: a website badge and a smaller README badge. The badge shows the uptime percentage over the last 30 days (7 on Free) and whether the site is up right now, updates itself every few minutes, and never shows the monitor’s name or address. Turning it off removes it everywhere it is embedded.' },
  { q: 'How do I avoid false alerts during planned maintenance?', a: 'Schedule a maintenance window on a Pro or Agency plan. For all or some of your monitors, once or repeating daily or weekly, HawkEye keeps checking but does not open incidents or send alerts, and leaves those checks out of uptime. An incident that is already open stays open until the monitor recovers.' },
  { q: 'Can HawkEye tell when my page loads but shows an error?', a: 'Yes, on every plan. Some failures still answer “200 OK”: a WordPress database error, a hosting “Account Suspended” page, a server’s default page, or an expired domain that has been parked. In a monitor’s “Page content” section, add text the page must contain (such as “Add to cart” or your company name) and text it must not contain, or pick a common problem from the list with one click. If the check fails you are alerted just as for an outage.' },
  { q: 'Can I monitor an API?', a: 'Yes. Choose the request method, add custom headers and a request body, and authenticate with Basic, a bearer token or an API-key header. You can then check the status code, look for text in the response, or verify a value in a JSON response. A multi-step API check runs several requests in a row, such as signing in and then fetching an account with the token it returned, and tells you which step failed. Passwords, tokens and API keys are stored encrypted and are never shown again. Methods that change data (POST, PUT, PATCH and DELETE) are repeated on every check, so they require a verified email address and run at most every 5 minutes.' },
  { q: 'Can HawkEye monitor cron jobs and scheduled tasks?', a: 'Yes, on every plan. Create a heartbeat monitor and HawkEye gives it a private ping address. Add one line to the end of your cron job, backup script, scheduled task or queue worker so it requests that address each time it runs. If a ping does not arrive on schedule (you choose how often to expect one and how much grace time to allow), or the job reports a failure or a non-zero exit status, HawkEye opens an incident and alerts you like it does for a website that is down. Heartbeat monitors count toward your plan’s monitor limit. See the heartbeat monitoring guide in our docs for examples.' },
  { q: 'Can HawkEye monitor my DNS records?', a: 'Yes, on every plan. Create a DNS monitor for a host name and choose an A, AAAA, MX, NS or TXT record. HawkEye looks it up on a schedule and alerts you when the record disappears or, if you enter the value you expect, when it stops matching: a mail server that changed, or an SPF or DMARC record that was removed. Each DNS monitor counts toward your plan’s monitor limit.' },
  { q: 'Does HawkEye have an API?', a: 'Yes. Create an API key in your account and use it from your own scripts, dashboards and tools to read your monitors, uptime and incidents, on every plan. A read and write key can also add monitors on every plan, and on Pro and Agency make other changes, such as pausing monitors during a deploy. Each key is read-only or read and write, expires after 90 days unless you choose another date (or no expiry), and can be revoked at any time. Account, billing and team settings always need you to sign in, so a key cannot be used to take over your account. See the API guide in our docs.' },
  { q: 'Can I use HawkEye with Claude or another AI assistant?', a: 'Yes. HawkEye is listed in Claude’s connector directory: add it in Claude, sign in to HawkEye and choose what Claude may do. Claude Code and other MCP clients can also connect to HawkEye’s MCP server with an API key. Either way, the assistant can answer questions such as “what’s down right now?” or “how was uptime this month?”, and suggest monitors for your site. With read and write access it can also add monitors on every plan, and on Pro or Agency pause monitors, post incident updates and schedule maintenance; Claude asks you before posting anything public, and never sees the passwords a check uses. The assistant acts as you, with your permissions, and can never reach your password, billing or team settings. See the Claude guide in our docs.' },
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
        <Link className="button primary" to="/register">Start free</Link>
      ) : billingEnabled ? (
        <Link className="button primary" to={signedIn ? (interval === 'year' ? '/billing?interval=year' : '/billing') : '/register'}>{signedIn ? `Choose ${plan.name}` : `Start free, upgrade to ${plan.name}`}</Link>
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
  // What paid plans add over Free, in words with no numbers (the cards carry those). Keep it in step with PlanDetails when a plan feature is added.
  const paidAdvantages =
    `Paid plans add more monitors, faster checks, longer history, a higher domain limit, ${multiLocation ? 'checks from several locations, ' : ''}maintenance windows, more multi-step API checks, monthly PDF uptime reports, AI-drafted incident updates and API keys that can make every change. ` +
    (multiSeat ? 'Agency also adds team members and reports with your own name and logo.' : 'Agency adds reports with your own name and logo.')

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
            Know when your websites go offline, spot changes in response times, and keep every monitor in view from one simple
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
            <li className="d-badge d-badge-soft d-badge-neutral">Outages confirmed before alerts</li>
            <li className="d-badge d-badge-soft d-badge-warning">SSL expiry warnings</li>
            <li className="d-badge d-badge-soft d-badge-info">Performance history</li>
            <li className="d-badge d-badge-soft d-badge-accent">Public status page</li>
            <li className="d-badge d-badge-soft d-badge-primary">Cron job and DNS monitoring</li>
            <li className="d-badge d-badge-soft d-badge-secondary">Multi-step API checks</li>
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

      <section aria-labelledby="how-h" className="section how-it-works">
        <p className="eyebrow">How it works</p>
        <h2 id="how-h">From a failed check to the right people.</h2>
        <p>
          When a check fails, HawkEye checks again before raising the alarm, so a single blip never wakes anyone. Once the outage is confirmed it alerts you
          by email, chat or webhook, and tells you again when it’s over.
        </p>
        <MonitoringFlow />
      </section>

      <ProductShots />

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

      <section className="section split" aria-labelledby="truth-h">
        <img src={brand.icons.websites} alt="" width={170} height={170} />
        <div>
          <p className="eyebrow">Checks that tell the truth</p>
          <h2 id="truth-h">A failed check is not an incident.</h2>
          <p>
            A site counts as offline only after consecutive failed checks, and as recovered only after consecutive successful ones. You get one alert when an
            outage is confirmed and one when it ends.
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

      <ComingSoon />

      <section aria-labelledby="pricing-h" className="section">
        <h2 id="pricing-h">Simple plans. Clear visibility.</h2>
        <p className="hint">
          Start free. Upgrade when your monitoring needs grow. Every plan includes website and API monitoring (with a multi-step API check), SSL certificate expiry warnings, domain expiry warnings (up to a limit that grows with the plan), incident tracking, alerts by email, Slack, Microsoft Teams, Discord, Telegram and webhook, and public status pages (one on Free, more on paid plans). {paidAdvantages} The free plan is a real plan, not a trial, and monitors are never deleted if you move to a smaller plan.
        </p>
        {plans === null && (
          <div role="status" aria-label="Loading plans" className="plans-loading">
            <div className="d-skeleton h-56 w-full" />
            <div className="d-skeleton h-56 w-full" />
            <div className="d-skeleton h-56 w-full" />
          </div>
        )}
        {plans?.length === 0 && <p>Pricing is temporarily unavailable. You can still <Link to="/register">start for free</Link>.</p>}
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
          <Link className="d-link d-link-hover" to="/docs">Docs</Link>
          <Link className="d-link d-link-hover" to="/tools">Free tools</Link>
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
          <a className="d-link d-link-hover" href={STATUS_URL}>HawkEye status</a>
          <Link className="d-link d-link-hover" to="/privacy">Privacy policy</Link>
          <Link className="d-link d-link-hover" to="/terms">Terms of service</Link>
          <CookieSettingsButton className="d-link d-link-hover" />
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
