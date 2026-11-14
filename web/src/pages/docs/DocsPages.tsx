import { ArrowsRightLeftIcon, BellAlertIcon, ChatBubbleLeftRightIcon, ClockIcon, CodeBracketIcon, GlobeAltIcon, RocketLaunchIcon } from '@heroicons/react/24/outline'
import type { ComponentType, ReactNode, SVGProps } from 'react'
import { Link, NavLink } from 'react-router'
import { useAuth } from '../../auth'
import { brand, STATUS_URL } from '../../brand'
import { CookieSettingsButton } from '../../components/CookieBanner'

/** A docs article. KEEP IN STEP with the routes in App.tsx, the pages in seo.ts and the @app pattern in deploy/Caddyfile. */
interface Doc {
  path: string
  title: string
  summary: string
  icon: ComponentType<SVGProps<SVGSVGElement>>
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
        HawkEye watches your websites, APIs, scheduled jobs and DNS records and tells you when something goes wrong, before your customers notice. This page gets
        you from a new account to your first alert in a few minutes.
      </p>
      <h2 id="account">1. Create your account</h2>
      <p>
        <Link to="/register">Sign up free</Link> with your email address. The Free plan needs no card. Verify your email address when the message
        arrives.
      </p>
      <h2 id="monitor">2. Add a monitor</h2>
      <p>
        The quickest start is <strong>Set up from your domain</strong> (on the dashboard and the <strong>Monitors</strong> page). Enter your domain and
        HawkEye looks at your site from the outside and suggests monitors: your home page, a health endpoint such as <code>/healthz</code> if it has one, an
        API on an <code>api.</code> subdomain, and your email’s MX records. Untick any you don’t want; nothing is added until you confirm. If you use Claude
        Code, <Link to="/docs/claude#setup">HawkEye’s plugin</Link> goes further: it reads your repository, finds your cron jobs and scheduled tasks, and adds
        a heartbeat for each one.
      </p>
      <p>To add one yourself, choose <strong>Add monitor</strong> on the <strong>Monitors</strong> page, then pick what to watch:</p>
      <ul>
        <li>
          <strong>A website or API.</strong> Enter its address. HawkEye requests it on a schedule (every 5 minutes on Free, every minute on paid plans)
          and opens an incident when the failure is confirmed by consecutive failed checks, so a single blip never alerts anyone. You can also
          check for words on the page, check JSON values, send headers or authentication, get warned about slow responses, and send a trace context header so each check shows up in your tracing tool.
        </li>
        <li>
          <strong>A cron job or scheduled task.</strong> Your job pings HawkEye each time it runs, and HawkEye tells you when a ping is late or the job
          reports a failure. See <Link to="/docs/cron-job-monitoring">Heartbeat monitoring</Link>.
        </li>
        <li>
          <strong>A DNS record.</strong> HawkEye looks up a record on a schedule and tells you when it disappears or stops matching the value you expect.
          See <Link to="/docs/dns-monitoring">DNS monitoring</Link>.
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
        <li><strong>Status pages</strong> show visitors which of your services are up, without revealing their addresses or error details. During an incident, post updates from the <strong>Incidents</strong> page to tell them what is happening. On Pro and Agency, the owner can turn on AI-drafted updates there: a <strong>Draft with AI</strong> button writes a first draft for you to edit before posting.</li>
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

function DnsMonitoring() {
  return (
    <>
      <p>
        A website can be running perfectly and still be unreachable because a DNS record changed. A DNS monitor looks up one record of a host name on a
        schedule and alerts you when it disappears or stops matching the value you expect. DNS monitors are on every plan, and each one counts toward
        your plan’s monitor limit.
      </p>

      <h2 id="create">Create a DNS monitor</h2>
      <ol>
        <li>On the <strong>Monitors</strong> page choose <strong>Monitor a DNS record</strong>.</li>
        <li>Enter a public host name, such as <code>example.com</code> or <code>_dmarc.example.com</code>.</li>
        <li>Choose the record type.</li>
        <li>Optionally enter the value you expect. Without one, any answer counts as healthy, and you are alerted only if the record disappears.</li>
      </ol>

      <h2 id="records">What each record type checks</h2>
      <ul>
        <li><strong>A and AAAA</strong> (IPv4 and IPv6 addresses): one of the addresses must equal the one you enter.</li>
        <li><strong>MX</strong> (mail servers): one of the mail servers must equal the one you enter. Upper and lower case do not matter.</li>
        <li><strong>NS</strong> (name servers): one of the name servers must equal the one you enter. Upper and lower case do not matter.</li>
        <li>
          <strong>TXT</strong>: one of the records must contain the text you enter, in the same upper and lower case. Enter just the part you care about,
          such as <code>p=reject</code> for <code>_dmarc.example.com</code>.
        </li>
      </ul>
      <p>
        CNAME records cannot be checked directly. To watch a name that uses one, use an A or AAAA monitor on the same name: it follows the CNAME and checks
        the addresses it leads to.
      </p>

      <h2 id="examples">Useful things to watch</h2>
      <ul>
        <li><strong>Your website’s address</strong>: an A record, with the address of your server, to catch a change you did not make.</li>
        <li><strong>Your mail</strong>: an MX record, so a change that would stop your email arriving does not go unnoticed.</li>
        <li><strong>Your name servers</strong>: an NS record, the first thing to change when a domain moves or is taken over.</li>
        <li><strong>Email authentication</strong>: a TXT record on your domain for SPF, or on <code>_dmarc.example.com</code> for DMARC.</li>
      </ul>

      <h2 id="alerts">How problems are confirmed</h2>
      <p>
        A DNS problem is treated like a website outage: it opens an incident only after consecutive failed checks, and from most locations when a monitor is
        checked from several. <Link to="/docs/alerts">Routing by tag</Link>, alert delay, quiet hours and maintenance windows all apply, and a DNS monitor
        can appear on status pages and have an uptime badge. The domain it belongs to is also added to your <strong>Domains</strong> page, so you are warned
        before its registration lapses, up to your plan’s domain limit.
      </p>
      <p>
        Alerts name the value you expected. They never include the answer HawkEye received, so look the record up yourself or ask your DNS provider when
        you investigate.
      </p>

      <h2 id="limits">Good to know</h2>
      <ul>
        <li>
          <strong>Changes are not instant.</strong> HawkEye asks a DNS resolver, as your visitors’ devices do, so a change shows up once the
          old record expires from caches (its TTL). Planned changes can show as failing until you update the expected value.
        </li>
        <li>
          <strong>Addresses must be public.</strong> Names on private networks cannot be checked, and a name that resolves to a private address fails with
          “Destination is not permitted”.
        </li>
        <li>
          <strong>The lookup time is not your DNS provider’s speed.</strong> It is often near zero because the answer was cached, so slow-response alerts do
          not apply to DNS monitors.
        </li>
      </ul>
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
        <li><strong>DNS records</strong> that disappear or stop matching the value you expect (<Link to="/docs/dns-monitoring">DNS monitors</Link>).</li>
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

function ApiWorkflows() {
  return (
    <>
      <p>
        A website check tells you an address answers. A multi-step API check tells you something your customers do still works: it sends several
        requests in a row, such as signing in and then fetching an account with the token the sign-in returned, and fails at the first step that fails.
        Every plan includes one; paid plans include more and longer ones. Each counts toward your plan’s monitor limit.
      </p>

      <h2 id="create">Create one</h2>
      <ol>
        <li>Choose <strong>Add monitor</strong>, then <strong>A multi-step API check</strong>.</li>
        <li>For each step, give it a name, a method and an address. <strong>More options</strong> has headers, a body, authentication and checks on the response.</li>
        <li>Add the passwords and keys the steps need under <strong>Secrets</strong>.</li>
        <li>Choose <strong>Run test</strong> to try it before saving, then <strong>Add monitor</strong>.</li>
      </ol>

      <h2 id="values">Passing a value to the next step</h2>
      <p>
        Under <strong>Save values for later steps</strong>, a step can keep a value from its response: a JSON path such as <code>data.token</code>, a response
        header such as <code>Location</code>, or a cookie. Give it a name, such as <code>token</code>, and later steps use it by writing{' '}
        <code>{'{{token}}'}</code> in their address, header values, body or authentication. A secret is written <code>{'{{secret.password}}'}</code>. The
        editor lists the values each step can use; click one to copy it.
      </p>
      <p>An example, signing in and then checking the account:</p>
      <ol>
        <li>
          <strong>Sign in</strong>: <code>POST https://api.example.com/login</code> with the body{' '}
          <code>{'{"email": "monitor@example.com", "password": "{{secret.password}}"}'}</code>, saving <code>data.token</code> as <code>token</code>.
        </li>
        <li>
          <strong>Get account</strong>: <code>GET https://api.example.com/account</code> with a bearer token of <code>{'{{token}}'}</code>, expecting status 200
          and a JSON check that <code>plan</code> equals <code>active</code>.
        </li>
      </ol>
      <p>
        Values are inserted as text. In a JSON body put them inside quotes, as above; HawkEye escapes them so the body stays valid JSON. In an address
        they are encoded. A step’s host must be written out in full: values can go in the path and query, never in the host.
      </p>
      <p>
        If the site signs in with a session cookie instead of a token, turn on <strong>Send cookies from earlier steps</strong>. Cookies last for one run
        and are never stored.
      </p>

      <h2 id="results">Reading a result</h2>
      <p>
        <strong>Run test</strong> and the monitor’s <strong>Latest run</strong> show each step with its status code and time, a ✕ where the run stopped,
        and the reason. Alerts name the failing step, for example “Step 2 (Get account): Received HTTP 401, expected 200”. When the problem is the check
        itself, such as a value that no earlier step saves, the result says the test needs fixing, so a broken test reads differently from a broken API.
        Slow-response alerts use the time of all the steps together.
      </p>

      <h2 id="safety">Secrets and safety</h2>
      <ul>
        <li>Secrets and step credentials are stored encrypted, never shown again, and hidden as <code>[hidden]</code> in results and alerts, along with every value a step saves.</li>
        <li>HawkEye keeps only each step’s name, status code, time and failure reason, never the responses.</li>
        <li>Use a test account made for monitoring, not a real person’s, and give it as little access as it needs.</li>
        <li>
          Steps that use POST, PUT, PATCH or DELETE are repeated on every check. They need a verified email address, the check runs at most every 5 minutes,
          and they should only reach endpoints that are safe to call again and again.
        </li>
        <li>A run may take up to 60 seconds in total; <strong>Run test</strong> stops after 25.</li>
      </ul>

      <h2 id="api">From the API</h2>
      <p>
        Create one with <code>POST /api/v1/monitors</code> and <code>"type": "workflow"</code>, a list of <code>steps</code> and a <code>secrets</code>{' '}
        object. See the <Link to="/docs/api">API guide</Link> for keys.
      </p>
    </>
  )
}

function ApiDocs() {
  return (
    <>
      <p>
        HawkEye’s API lets your own scripts, dashboards and tools read your monitors, uptime and incidents, and on Pro and Agency make changes too: add
        monitors, pause them during a deploy, post an update on an incident. It is the same API the HawkEye app uses, with an API key instead of a sign-in.
      </p>

      <h2 id="keys">1. Create an API key</h2>
      <p>
        Open <strong>API keys</strong> from the account menu (the person icon) and choose <strong>Create a key</strong>. Give it a name you will recognize,
        choose its access and when it expires, and copy the key. It starts with <code>hk_</code> and is shown only once: HawkEye keeps just a fingerprint of it.
      </p>
      <ul>
        <li><strong>Read only</strong> keys can list and read everything below. They work on every plan, including Free.</li>
        <li><strong>Read and write</strong> keys can also make changes. On Free they can add monitors (within the plan’s limits) and nothing else; on Pro and Agency they can make every change the API offers. After a downgrade a key keeps reading and adding monitors, and stops making other changes.</li>
      </ul>
      <p>
        A key belongs to you and to the organization you made it in. It can do what you can do there and no more: if you are a member rather than the owner,
        owner-only actions are refused, and if you leave the organization your keys stop working.
      </p>

      <h2 id="requests">2. Send it with each request</h2>
      <p>Put the key in the <code>Authorization</code> header. Requests and responses are JSON.</p>
      <Code>{`curl https://hawkeye.observer/api/v1/monitors \\
  -H "Authorization: Bearer $HAWKEYE_API_KEY"`}</Code>
      <p>Pause a monitor before a deploy and resume it afterward (needs a read and write key):</p>
      <Code>{`curl -X POST https://hawkeye.observer/api/v1/monitors/<monitor-id>/pause \\
  -H "Authorization: Bearer $HAWKEYE_API_KEY"

curl -X POST https://hawkeye.observer/api/v1/monitors/<monitor-id>/resume \\
  -H "Authorization: Bearer $HAWKEYE_API_KEY"`}</Code>
      <p>Add a website monitor:</p>
      <Code>{`curl -X POST https://hawkeye.observer/api/v1/monitors \\
  -H "Authorization: Bearer $HAWKEYE_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"name": "Shop", "url": "https://shop.example.com", "check_interval_seconds": 60}'`}</Code>

      <p>To use HawkEye from Claude or another AI assistant instead, see <Link to="/docs/claude">Use HawkEye with Claude</Link>.</p>

      <h2 id="what">What a key can reach</h2>
      <ul>
        <li><strong>Monitors</strong>: list, read, add, edit, delete, pause and resume them, tags and bulk actions, results, uptime and response times.</li>
        <li><strong>Incidents</strong> and the updates posted on them (posting an update needs the owner, as in the app).</li>
        <li><strong>Status pages, maintenance windows and domains</strong>, the dashboard summary, check locations and your plan’s usage.</li>
      </ul>
      <p>
        Some things always need you to sign in to HawkEye, so a leaked key can’t be used to take over your account: your password and account settings,
        billing, your team and invitations, alert destinations, and API keys themselves. A key gets <code>403 SESSION_REQUIRED</code> there.
      </p>

      <h2 id="expiry">Expiry and revoking</h2>
      <p>
        Keys expire after 90 days unless you choose 30 days, a year, a date of your own, or no expiry. HawkEye emails you a week before a key expires and
        again when it does. An expired key can’t be extended: create a new one and replace it. Revoke a key on the API keys page at any time; it stops
        working at once. Resetting a forgotten password also revokes your keys, in case someone else made them.
      </p>

      <h2 id="errors">Errors and limits</h2>
      <p>Errors come back as <code>{'{"error": {"code": "…", "message": "…"}}'}</code> with a message you can show to a person. The ones specific to keys:</p>
      <ul>
        <li><code>401 UNAUTHENTICATED</code>: the key is wrong, incomplete or revoked.</li>
        <li><code>401 TOKEN_EXPIRED</code>: the key has passed its expiry date.</li>
        <li><code>403 TOKEN_SCOPE</code>: a read-only key tried to change something.</li>
        <li><code>403 PLAN_LIMIT_REACHED</code>: the plan doesn’t include the change (for example, writing on Free).</li>
        <li><code>429 RATE_LIMITED</code>: more than 120 requests, or 30 changes, in a minute with one key. Wait for the number of seconds in <code>Retry-After</code>.</li>
      </ul>

      <h2 id="safety">Keeping keys safe</h2>
      <ul>
        <li>Use a read-only key wherever you only need to read, and one key per tool, so you can revoke one without breaking the rest.</li>
        <li>Keep keys in a secret store or environment variable, never in code you commit. The <code>hk_</code> start makes a leaked key easy to spot.</li>
        <li>The API keys page shows when each key was last used. Revoke any you don’t recognize; HawkEye also emails you whenever a key is created.</li>
        <li>Every change made with a key is in the organization’s <strong>security log</strong> (account menu, for the owner), with the key’s name, so you can see what a key did.</li>
      </ul>
    </>
  )
}

function ClaudeDocs() {
  return (
    <>
      <p>
        HawkEye has an MCP server, so Claude and other AI assistants that support the Model Context Protocol can read your monitoring and, if you allow
        it, make changes for you. Ask “what’s down right now?”, “how was uptime for the shop this month?” or “pause the Acme monitors while I deploy”.
      </p>

      <h2 id="connect-claude">Connect Claude (web, desktop and mobile)</h2>
      <p>
        HawkEye is <a href="https://claude.ai/directory/hawkeye" target="_blank" rel="noopener noreferrer">listed in Claude’s connector directory</a>, so
        there is nothing to install and no key to copy:
      </p>
      <ol>
        <li>
          Open <a href="https://claude.ai/directory/hawkeye" target="_blank" rel="noopener noreferrer">HawkEye in Claude’s directory</a>, or in Claude
          open <strong>Customize</strong>, then <strong>Connectors</strong>, and search for <strong>HawkEye</strong>.
        </li>
        <li>Choose <strong>Connect</strong>.</li>
        <li>
          Sign in to HawkEye (with your two-factor code if you use one), choose the organization Claude may use, and choose <strong>Read only</strong>, or{' '}
          <strong>Read and write</strong> to let Claude make changes too (on Free, only adding monitors).
        </li>
      </ol>
      <p>
        On a Claude Team or Enterprise plan, an owner of your Claude organization adds HawkEye from the organization’s connector settings first; then each
        person connects their own HawkEye account. You can disconnect at any time from Claude or from <strong>Connected apps</strong> on HawkEye’s API keys page.
      </p>

      <h2 id="connect">Connect Claude Code</h2>
      <p>
        Create an <Link to="/docs/api">API key</Link> (account menu, then <strong>API keys</strong>). Choose <strong>Read only</strong> to let Claude look but not
        touch, or <strong>Read and write</strong> to let it make changes too (on Free, only adding monitors). Then run:
      </p>
      <Code>{`claude mcp add --transport http hawkeye https://hawkeye.observer/mcp \\
  --header "Authorization: Bearer $HAWKEYE_API_KEY"`}</Code>
      <p>
        Other MCP clients that can send a header with each request work the same way: the server address is <code>https://hawkeye.observer/mcp</code> and the
        key goes in the <code>Authorization</code> header. Some apps connect by signing in instead, as Claude does: they send you to a HawkEye page where you
        choose the organization and the access the app gets (and confirm with your two-factor code if you use one).
      </p>

      <h2 id="setup">Set up monitoring from your code</h2>
      <p>
        HawkEye’s plugin for Claude Code sets up monitoring from your repository. It finds your production domain, your cron jobs and scheduled tasks (crontab,
        GitHub Actions schedules, Kubernetes CronJobs, platform crons and in-app schedulers) and your health endpoints, asks HawkEye to suggest monitors for
        your live site, and shows you one plan. Once you approve it, Claude creates the monitors, including a heartbeat for each job, and adds the ping to the
        end of each job. Ping addresses are read from environment variables or CI secrets, never written into your code, and Claude tells you which to set.
      </p>
      <p>
        It needs a <strong>Read and write</strong> key, which can add monitors on every plan, including Free. Run these in
        Claude Code (the plugin connects to HawkEye for you, so you don’t need the command above as well); it asks for the key when the plugin is enabled
        and keeps it in your system’s secure credential store. To change the key later, run <code>claude plugin configure hawkeye</code>.
      </p>
      <Code>{`/plugin marketplace add EssenceOfChaos/hawkeye
/plugin install hawkeye@hawkeye`}</Code>
      <p>
        Then ask “set up monitoring for this project”. Your code stays on your machine; HawkEye receives only the monitors you approve. Other coding agents
        that read skills can use the same <a href="https://github.com/EssenceOfChaos/hawkeye/tree/main/plugins/hawkeye">skill</a> with HawkEye’s MCP server
        configured as above.
      </p>

      <h2 id="tools">What Claude can do</h2>
      <ul>
        <li><strong>Read</strong> (any key): an account overview, your monitors and their state, uptime and response times, incidents and their updates, status pages and maintenance windows.</li>
        <li><strong>Add monitors</strong> (website, heartbeat or DNS) with a read and write key, on every plan.</li>
        <li><strong>Pause and resume monitors</strong>, one by one or every monitor with a tag (Pro and Agency).</li>
        <li>
          <strong>Suggest monitors for a domain</strong> (any key): Claude asks HawkEye to look at your site, as <Link to="/docs/getting-started#monitor">Set up from
          your domain</Link> does, and offers the suggestions to you before adding any.
        </li>
        <li><strong>Post updates on incidents</strong> and <strong>schedule maintenance</strong> (the organization’s owner only, as in the app), and delete them again.</li>
        <li>
          <strong>Set up and test <Link to="/docs/api-workflows">multi-step API checks</Link></strong>. Claude never sees passwords or keys: it names the
          secrets a check needs, and the check stays paused until you enter them in HawkEye. Claude asks before saving one, because its steps call your API
          on every run.
        </li>
      </ul>
      <p>
        Claude acts as you, in the organization the key belongs to, with your permissions, and no more. It can’t see or change your password, billing, team,
        alert destinations or API keys. It never sees monitor credentials, custom headers or request bodies. Every change Claude makes is in the
        organization’s security log, with the key or connected app it used.
      </p>

      <h2 id="confirm">Claude asks before public or destructive changes</h2>
      <p>
        Posting an incident update publishes it on your status pages at once, so it is marked as a change Claude must ask you about first, along with
        deleting an update or a maintenance window. You see exactly what will be posted before it is. Reading never needs your approval; other changes
        follow your Claude permission settings.
      </p>

      <h2 id="safety">Good to know</h2>
      <ul>
        <li>Apps you connected by signing in are listed under <strong>Connected apps</strong> on the API keys page, where you can disconnect them; HawkEye emails you whenever one is connected.</li>
        <li>A key used with Claude is an ordinary API key: it expires (after 90 days unless you chose otherwise), shows when it was last used, and you can revoke it at any time.</li>
        <li>Changes count toward the key’s limit of 30 a minute, shared with the API.</li>
        <li>Monitor names, incident summaries and updates are text people wrote or that came from your sites; HawkEye tells Claude to treat them as data, not instructions.</li>
      </ul>
    </>
  )
}

export const DOCS: readonly Doc[] = [
  { path: '/docs/getting-started', title: 'Getting started', summary: 'From a new account to your first monitor and alert in a few minutes.', icon: RocketLaunchIcon, body: GettingStarted },
  { path: '/docs/cron-job-monitoring', title: 'Heartbeat (cron job) monitoring', summary: 'Get alerted when a backup, scheduled task or queue worker stops running or fails.', icon: ClockIcon, body: HeartbeatMonitoring },
  { path: '/docs/api-workflows', title: 'Multi-step API checks', summary: 'Check that a sequence of requests works, such as signing in and fetching an account, and see which step failed.', icon: ArrowsRightLeftIcon, body: ApiWorkflows },
  { path: '/docs/dns-monitoring', title: 'DNS monitoring', summary: 'Get alerted when a DNS record disappears or stops matching the value you expect.', icon: GlobeAltIcon, body: DnsMonitoring },
  { path: '/docs/alerts', title: 'Alerts', summary: 'Alert channels, routing alerts by client, alert delays, quiet hours and maintenance windows.', icon: BellAlertIcon, body: Alerts },
  { path: '/docs/api', title: 'API and API keys', summary: 'Use HawkEye from your own scripts and tools: create a key, send requests, and keep keys safe.', icon: CodeBracketIcon, body: ApiDocs },
  { path: '/docs/claude', title: 'Use HawkEye with Claude', summary: 'Connect Claude from its connector directory, or Claude Code with an API key, to check what is down, look at uptime, pause monitors and set up monitoring from your code by asking.', icon: ChatBubbleLeftRightIcon, body: ClaudeDocs },
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
          <ul className="d-menu">
            {DOCS.map((d) => (
              <li key={d.path}>
                <NavLink to={d.path} className={({ isActive }) => (isActive ? 'd-menu-active' : undefined)}>
                  <d.icon />
                  {d.title}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main>{children}</main>
      </div>
      <p className="hint">
        <Link to="/">Home</Link> · <Link to="/tools">Free tools</Link> · <Link to="/contact">Contact us</Link> · <a href={STATUS_URL}>HawkEye status</a> ·{' '}
        <Link to="/privacy">Privacy policy</Link> ·{' '}
        <Link to="/terms">Terms of service</Link> · <CookieSettingsButton />
      </p>
    </div>
  )
}

export function DocsIndexPage() {
  return (
    <DocsShell>
      <h1>HawkEye documentation</h1>
      <p className="tool-intro">How to monitor websites, APIs, scheduled jobs and DNS records with HawkEye, and how to get the right alerts to the right people.</p>
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
