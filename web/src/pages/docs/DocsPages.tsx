import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router'
import { useAuth } from '../../auth'
import { brand } from '../../brand'
import { CookieSettingsButton } from '../../components/CookieBanner'

/** A docs article. KEEP IN STEP with the routes in App.tsx, the pages in seo.ts and the @app pattern in deploy/Caddyfile. */
interface Doc {
  path: string
  title: string
  summary: string
  body: () => ReactNode
}

const PING = 'https://hawkeye.observer/ping/<your-token>'

function Code({ children }: { children: string }) {
  return <pre className="doc-code"><code>{children}</code></pre>
}

function GettingStarted() {
  return (
    <>
      <p>
        HawkEye watches your websites, APIs and scheduled jobs and tells you when something goes wrong, before your customers notice. This page gets
        you from a new account to your first alert in a few minutes.
      </p>
      <h2 id="account">1. Create your account</h2>
      <p>
        <Link to="/register">Sign up free</Link> with your email address. The Free plan needs no card. Verify your email address when the message
        arrives.
      </p>
      <h2 id="monitor">2. Add a monitor</h2>
      <p>On the <strong>Monitors</strong> page choose <strong>Add monitor</strong>, then pick what to watch:</p>
      <ul>
        <li>
          <strong>A website or API.</strong> Enter its address. HawkEye requests it on a schedule (every 5 minutes on Free, every minute on paid plans)
          and opens an incident when the failure is confirmed by consecutive failed checks, so a single blip never alerts anyone. You can also
          check for words on the page, check JSON values, send headers or authentication, and get warned about slow responses.
        </li>
        <li>
          <strong>A cron job or scheduled task.</strong> Your job pings HawkEye each time it runs, and HawkEye tells you when a ping is late or the job
          reports a failure. See <Link to="/docs/cron-job-monitoring">Heartbeat monitoring</Link>.
        </li>
      </ul>
      <p>Every monitor counts toward your plan’s monitor limit, whichever kind it is.</p>
      <h2 id="alerts">3. Decide who hears about it</h2>
      <p>
        Your own email address receives alerts from the start. On the <strong>Alerts</strong> page you can add more email addresses and connect
        Slack, Microsoft Teams, Discord, Telegram or a webhook, then send a test alert to make sure it arrives. See <Link to="/docs/alerts">Alerts</Link>{' '}
        for routing alerts by client, alert delays and quiet hours.
      </p>
      <h2 id="share">4. Share the status (optional)</h2>
      <ul>
        <li><strong>Status pages</strong> show visitors which of your services are up, without revealing their addresses or error details.</li>
        <li><strong>Uptime badges</strong> put a live uptime figure on a website or in a README. Turn one on from a monitor’s page.</li>
        <li><strong>Monthly PDF reports</strong> (Pro and Agency) summarize each site’s uptime for clients or managers.</li>
      </ul>
      <h2 id="certificates">Certificates and domains</h2>
      <p>
        HTTPS monitors also watch their site’s certificate and warn you 30, 14, 7 and 1 days before it expires. The <strong>Domains</strong> page warns
        you before a domain registration lapses.
      </p>
    </>
  )
}

function HeartbeatMonitoring() {
  return (
    <>
      <p>
        Backups, invoice runs, data imports, queue workers and other scheduled jobs tend to fail silently: nothing is down, the job just quietly
        stops. A heartbeat monitor turns that around. Each time your job finishes, it requests a secret ping address. If a ping does not arrive in
        time, or the job reports that it failed, HawkEye opens an incident and alerts you like it does for a website that is down.
      </p>
      <p>Heartbeat monitors are available on every plan, including Free, and count toward your plan’s monitor limit like any other monitor.</p>

      <h2 id="create">Create a heartbeat monitor</h2>
      <ol>
        <li>On the <strong>Monitors</strong> page choose <strong>Monitor a cron job</strong> (or <strong>Add monitor</strong>, then <em>A cron job or scheduled task</em>).</li>
        <li>Name it after the job, for example “Nightly database backup”.</li>
        <li>
          Set <strong>Expect a ping every</strong> to how often the job runs: 1 day for a nightly job, 1 hour for an hourly one. Periods can be as short
          as your plan’s fastest check interval (5 minutes on Free, 1 minute on paid plans) and as long as 30 days.
        </li>
        <li>
          Set the <strong>Grace time</strong>: how late a ping may be before it counts as missed. Allow for the job’s usual run time and a little more.
          It can be from 1 minute to 7 days.
        </li>
        <li>Save. The monitor’s page shows its ping address and examples to copy.</li>
      </ol>
      <p>
        A new heartbeat waits for its first ping and never alerts before then, so you can set up the job at your own pace. From the first ping on, a
        ping is due one period after the last one, plus the grace time.
      </p>

      <h2 id="ping">Send pings</h2>
      <p>Request the ping address when the job has finished successfully. GET, HEAD and POST all work, and no login or headers are needed.</p>
      <Code>{`# crontab: run the backup at 02:00, then ping HawkEye if it succeeded
0 2 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 5 -o /dev/null ${PING}`}</Code>
      <p>
        <code>-m 10</code> stops curl from hanging, and <code>--retry 5</code> makes it try again if the network hiccups, so one lost request is not
        mistaken for a missed run.
      </p>

      <h3 id="failures">Report failures too</h3>
      <p>
        Waiting for a missed ping tells you about a failure only after the grace time. If the job can tell when it failed, report it straight away and
        HawkEye alerts at once:
      </p>
      <ul>
        <li><code>/ping/&lt;token&gt;/fail</code> reports a failure.</li>
        <li><code>/ping/&lt;token&gt;/&lt;exit status&gt;</code> reports the job’s exit status: <code>0</code> is success, <code>1</code> to <code>255</code> a failure.</li>
      </ul>
      <Code>{`# Always ping, with the backup's exit status (0 = success)
0 2 * * * /usr/local/bin/backup.sh; curl -fsS -m 10 --retry 5 -o /dev/null ${PING}/$?`}</Code>

      <h3 id="output">Send the job’s output</h3>
      <p>
        The body of a POST is kept with the ping (the first 1,000 characters) and shown in the monitor’s ping log, which is handy for seeing why a
        run failed without logging in to the server.
      </p>
      <Code>{`#!/bin/sh
output=$(/usr/local/bin/backup.sh 2>&1)
status=$?
printf '%s' "$output" | tail -c 1000 | curl -fsS -m 10 --retry 5 -o /dev/null --data-binary @- ${PING}/$status`}</Code>

      <h3 id="examples">Other schedulers and languages</h3>
      <p><strong>systemd timer</strong>: add to the service the timer starts.</p>
      <Code>{`[Service]
ExecStart=/usr/local/bin/backup.sh
ExecStartPost=/usr/bin/curl -fsS -m 10 --retry 5 -o /dev/null ${PING}`}</Code>
      <p><strong>Kubernetes CronJob</strong>: ping after the job’s command.</p>
      <Code>{`command: ["/bin/sh", "-c", "/app/report.sh && wget -q -T 10 -O /dev/null ${PING}"]`}</Code>
      <p><strong>GitHub Actions</strong> (scheduled workflow): add two final steps.</p>
      <Code>{`- name: Ping HawkEye
  if: success()
  run: curl -fsS -m 10 --retry 5 -o /dev/null ${PING}
- name: Report the failure to HawkEye
  if: failure()
  run: curl -fsS -m 10 --retry 5 -o /dev/null ${PING}/fail`}</Code>
      <p><strong>Windows Task Scheduler / PowerShell</strong></p>
      <Code>{`C:\\Jobs\\Export.ps1
Invoke-RestMethod -Uri "${PING}" -TimeoutSec 10`}</Code>
      <p><strong>Python</strong></p>
      <Code>{`import urllib.request
urllib.request.urlopen("${PING}", timeout=10)`}</Code>
      <p><strong>Node.js</strong> (18 or later)</p>
      <Code>{`await fetch("${PING}", { signal: AbortSignal.timeout(10_000) })`}</Code>

      <h2 id="alerts">What happens when a ping is missed</h2>
      <ul>
        <li>
          When the deadline passes, HawkEye records a missed run and opens an incident. The incident starts at the deadline, and your alert channels
          are told that the job did not check in on time.
        </li>
        <li>A failure ping opens an incident at once, with the exit status if you sent one.</li>
        <li>The next successful ping resolves the incident, and you get one recovery message.</li>
        <li>
          Everything you have set up for alerts applies: <Link to="/docs/alerts">routing by tag</Link>, alert delay, quiet hours and maintenance
          windows.
        </li>
      </ul>
      <p>
        Uptime for a heartbeat is the share of expected runs that pinged successfully: each ping counts as a success, and each missed period or failure
        ping as a failure. It appears on the monitor’s page, status pages, uptime badges and reports like any monitor’s uptime.
      </p>

      <h2 id="details">Good to know</h2>
      <ul>
        <li>
          <strong>Pausing.</strong> While a heartbeat is paused, pings are logged but change nothing. When you resume it, the job gets a full period
          and the grace time to ping before it can be called missed.
        </li>
        <li>
          <strong>Changing the period or grace time</strong> moves the next deadline, counted from the last ping.
        </li>
        <li>
          <strong>If HawkEye itself was unreachable</strong>, a ping your job sent at that moment could have been lost. HawkEye never blames your job
          for that: after any interruption on our side, each heartbeat gets a full period and its grace time before a ping can be called missed.
        </li>
        <li><strong>Ping log.</strong> The last 100 pings are kept, with their time, result, source address, user agent and any output sent.</li>
        <li><strong>Rate limit.</strong> A ping address accepts about one ping a second, far more than any schedule needs.</li>
        <li>
          <strong>Keep the address private.</strong> Anyone who has it can report that your job ran. If it leaks, use <em>Get a new ping address</em>{' '}
          on the monitor’s page; the old address stops working at once.
        </li>
      </ul>
      <h3 id="answers">What the ping address answers</h3>
      <table className="doc-table">
        <thead><tr><th>Response</th><th>Meaning</th></tr></thead>
        <tbody>
          <tr><td><code>200 OK</code></td><td>The ping was recorded (or logged only, if the monitor is paused).</td></tr>
          <tr><td><code>404</code></td><td>No heartbeat monitor has that address: check it, or the monitor was deleted or given a new address.</td></tr>
          <tr><td><code>429</code></td><td>Too many pings to this address in a short time. Wait a few seconds.</td></tr>
        </tbody>
      </table>
    </>
  )
}

function Alerts() {
  return (
    <>
      <p>HawkEye sends an alert when a problem is confirmed and one more when it is over. This page explains where alerts go and when.</p>

      <h2 id="channels">Where alerts go</h2>
      <ul>
        <li>
          <strong>Email.</strong> Your own address is set up when you sign up. Other addresses must confirm before they receive anything, so nobody is
          signed up for alerts by mistake.
        </li>
        <li><strong>Slack, Microsoft Teams and Discord</strong> through an incoming webhook for the channel you choose.</li>
        <li><strong>Telegram</strong> through HawkEye’s bot or your own.</li>
        <li><strong>Webhooks</strong>: a signed JSON request to your own endpoint, for anything else.</li>
      </ul>
      <p>Use <strong>Send test</strong> on the Alerts page to check that a destination works. A test alert says plainly that nothing is wrong.</p>

      <h2 id="what">What you are alerted about</h2>
      <ul>
        <li><strong>Outages and recoveries</strong> of websites and APIs, confirmed by consecutive failed checks (and by most locations, when a monitor is checked from several).</li>
        <li><strong>Cron jobs</strong> that miss a ping or report a failure (<Link to="/docs/cron-job-monitoring">heartbeat monitors</Link>).</li>
        <li><strong>Slow responses</strong>, if you turn this on for a monitor: when responses stay above your threshold for several checks in a row.</li>
        <li><strong>Certificates</strong> 30, 14, 7 and 1 days before they expire, and <strong>domains</strong> before their registration lapses.</li>
      </ul>

      <h2 id="routing">Send each client’s alerts to the right people</h2>
      <p>
        By default every destination receives alerts for every monitor. To route them, tag your monitors (for example by client) and, on the Alerts
        page, limit a destination to monitors with certain tags. A client’s Slack channel can then hear only about that client’s sites.
      </p>
      <p>
        An alert that no destination would receive is never dropped: it goes to your team members’ own email addresses instead, and the Alerts page
        warns you which monitors are not covered.
      </p>

      <h2 id="timing">Alert delay and quiet hours</h2>
      <p>Each monitor has its own alert timing, set on its edit page or for many monitors at once from the Monitors list.</p>
      <ul>
        <li>
          <strong>Alert delay</strong>: announce an outage only once it has lasted a while (for example 15 minutes, counted from the first failure). If
          the monitor recovers sooner, nobody is alerted about it at all.
        </li>
        <li>
          <strong>Quiet hours</strong>: a daily window, in a time zone you choose, during which the monitor’s alerts are held and sent when it ends. If
          the problem is over by then, nobody is woken for it. Use it for sites that can wait until morning.
        </li>
      </ul>

      <h2 id="maintenance">Maintenance windows</h2>
      <p>
        On Pro and Agency, schedule one-off, daily or weekly maintenance windows. During a window, monitors are still checked but raise no incidents
        or alerts, and the time does not count against uptime.
      </p>
    </>
  )
}

export const DOCS: readonly Doc[] = [
  { path: '/docs/getting-started', title: 'Getting started', summary: 'From a new account to your first monitor and alert in a few minutes.', body: GettingStarted },
  { path: '/docs/cron-job-monitoring', title: 'Heartbeat (cron job) monitoring', summary: 'Get alerted when a backup, scheduled task or queue worker stops running or fails.', body: HeartbeatMonitoring },
  { path: '/docs/alerts', title: 'Alerts', summary: 'Alert channels, routing alerts by client, alert delays, quiet hours and maintenance windows.', body: Alerts },
]

function DocsShell({ children }: { children: ReactNode }) {
  const { status } = useAuth()
  return (
    <div className="legal docs">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logoWhite} alt="HawkEye" width={160} height={32} /></Link>
        <Link to="/docs">Docs</Link>
        <span className="spacer" />
        {status === 'authenticated' ? <Link to="/dashboard">Dashboard</Link> : <Link to="/register">Start free</Link>}
      </header>
      <div className="docs-layout">
        <nav aria-label="Documentation" className="docs-nav">
          <ul>
            {DOCS.map((d) => <li key={d.path}><NavLink to={d.path}>{d.title}</NavLink></li>)}
          </ul>
        </nav>
        <main>{children}</main>
      </div>
      <p className="hint">
        <Link to="/">Home</Link> · <Link to="/tools">Free tools</Link> · <Link to="/contact">Contact us</Link> · <Link to="/privacy">Privacy policy</Link> ·{' '}
        <Link to="/terms">Terms of service</Link> · <CookieSettingsButton />
      </p>
    </div>
  )
}

export function DocsIndexPage() {
  return (
    <DocsShell>
      <h1>HawkEye documentation</h1>
      <p className="tool-intro">How to monitor websites, APIs and scheduled jobs with HawkEye, and how to get the right alerts to the right people.</p>
      <ul className="tool-list">
        {DOCS.map((d) => (
          <li key={d.path} className="card">
            <h2><Link to={d.path}>{d.title}</Link></h2>
            <p>{d.summary}</p>
          </li>
        ))}
      </ul>
      <p>Can’t find an answer? <Link to="/contact">Ask us</Link>.</p>
    </DocsShell>
  )
}

export function DocPage({ path }: { path: string }) {
  const doc = DOCS.find((d) => d.path === path)!
  return (
    <DocsShell>
      <article>
        <h1>{doc.title}</h1>
        <doc.body />
        <p className="hint">Questions or something missing? <Link to="/contact">Tell us</Link>.</p>
      </article>
    </DocsShell>
  )
}
