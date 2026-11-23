import { Link } from 'react-router'
import { CodeBlock } from '../../components/CodeBlock'

const PING = 'https://hawkeye.observer/ping/<your-token>'

/** A code example with a copy button (CodeBlock). */
export function Code({ children }: { children: string }) {
  return <CodeBlock>{children}</CodeBlock>
}

export function GettingStarted() {
  return (
    <>
      <p>
        HawkEye watches your websites, APIs, scheduled jobs and DNS records and tells you when something goes wrong, before your customers notice. This page gets
        you from a new account to your first alert in a few minutes.
      </p>
      <h2 id="account">1. Create your account</h2>
      <p>
        <Link to="/register">Sign up free</Link> with your email address, or choose <strong>Continue with Google</strong> or{' '}
        <strong>Continue with GitHub</strong>. The Free plan needs no card. With an email address, verify it when the message arrives; Google and GitHub
        have already verified theirs. You can use any of the three to sign in later, and add or remove them under <strong>Account</strong> →{' '}
        <strong>Sign-in methods</strong>.
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
          <strong>A website or API.</strong> Enter its address. HawkEye requests it on a schedule (every 5 minutes on Free, every minute on Pro, as often as every 30 seconds on Agency)
          and opens an incident when the failure is confirmed: a failed check is checked again 15 seconds later, so a single blip never alerts anyone. You can also
          check for words on the page, check JSON values (see <a href="#json-checks">Checking what an API returns</a>), send headers or authentication, get warned about slow responses, and send a trace context header so each check shows up in your tracing tool. For an API, choose <strong>Paste a cURL command</strong>: paste one
          from your API docs, a terminal or your browser’s developer tools (Copy as cURL), and HawkEye fills in the address, method, headers, body and
          authentication. It is read in your browser, and a token it finds goes into the encrypted authentication field rather than a plain header. Have an
          OpenAPI (3.x) or Swagger (2.0) file, or a Postman collection (v2.1)? Choose <strong>Import from OpenAPI or Postman</strong> on the{' '}
          <strong>Monitors</strong> page: HawkEye lists a check for each endpoint, with health endpoints and endpoints that read data ticked. Each checks the
          status code, and from an OpenAPI spec also the fields it marks as required. From a collection, its variables, auth and saved example responses
          are used, and you type the value of any variable the collection leaves empty, such as <code>{'{{baseUrl}}'}</code>. Endpoints that change data are listed but not ticked. Enter credentials once, fill in any values a path needs (such as a real ID), and
          add them; they’re tagged with the API’s name so you can filter, pause or remove them together.
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
      <h2 id="json-checks">Checking what an API returns</h2>
      <p>
        An API can answer 200 and still be unhealthy. Under <strong>Response checks</strong>, a JSON check reads a value at a path such as{' '}
        <code>database.status</code> or <code>data.items[0].state</code> and checks that it equals, doesn’t equal or contains something, exists, or, for
        numbers, is greater than, at least, less than or at most a number: <code>queue_size</code> is at most <code>500</code>, for example.
      </p>
      <p>
        You don’t have to type paths. Choose <strong>Send test request</strong> and HawkEye sends the request once and shows the response. Click a field
        to add a check for it, then change the value or comparison if you need to. The test shows whether each check passes. Nothing is saved until you
        save the monitor, and the response is never stored.
      </p>
      <h2 id="certificates">Certificates and domains</h2>
      <p>
        HTTPS monitors also watch their site’s certificate and warn you 30, 14, 7 and 1 days before it expires. The <strong>Domains</strong> page warns
        you before a domain registration lapses.
      </p>
      <h2 id="timings">Where the time goes</h2>
      <p>
        Every website and API check is timed in four parts, shown under the response-time chart on the monitor’s page as an average for the period,
        and for each check in its history:
      </p>
      <ul>
        <li><strong>DNS lookup</strong>: finding the server’s address. Slow here points to your DNS provider.</li>
        <li><strong>Connect</strong>: opening the connection. Slow here points to the network or a server far from the visitor.</li>
        <li><strong>TLS handshake</strong>: setting up HTTPS. Slow here points to the server’s certificate setup or an overloaded server.</li>
        <li>
          <strong>Server wait</strong> (time to first byte): from sending the request to the first byte of the answer. This is mostly your
          application’s own work, such as rendering a page or querying a database.
        </li>
      </ul>
      <p>
        With redirects, each part is added up over every hop, and the number of redirects is shown. Multi-step API checks show the parts for each step.
      </p>
    </>
  )
}

export function HeartbeatMonitoring() {
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

export function DnsMonitoring() {
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
        <li>
          Optionally enter the value you expect. Without one, any answer counts as healthy, and you are alerted if the record disappears, or (with
          change alerts, on by default) when it changes.
        </li>
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
        <li><strong>Your website’s address</strong>: an A record, to catch a change you did not make. Change alerts notice it without you entering the address.</li>
        <li><strong>Your mail</strong>: an MX record, so a change that would stop your email arriving does not go unnoticed.</li>
        <li><strong>Your name servers</strong>: an NS record, the first thing to change when a domain moves or is taken over.</li>
        <li><strong>Email authentication</strong>: a TXT record on your domain for SPF, or on <code>_dmarc.example.com</code> for DMARC.</li>
      </ul>

      <h2 id="changes">Change alerts</h2>
      <p>
        HawkEye remembers each record’s answer and alerts you when it changes, without you typing in what it should be: a new mail server, a different
        address for your website, name servers that aren’t yours, an SPF or DMARC record that was edited. It’s on for every DNS monitor, and you can turn
        it off under <strong>Change alerts</strong> on the monitor’s edit page.
      </p>
      <ul>
        <li>The first lookup sets what’s normal, silently. A change must show up in two checks in a row, and from most locations when a monitor is checked from several, before you’re alerted.</li>
        <li>
          Address records (A and AAAA) allow for rotation: CDNs and load balancers hand out addresses from a pool, so only an address not seen in the
          last 7 days counts as a change. Turn on <strong>Alert on any address change</strong> for a site with one fixed address. Mail, name server and
          TXT records are always compared exactly.
        </li>
        <li>
          The alert shows the record before and after. If the change was yours, open the monitor and choose <strong>This change was intended</strong>: the
          new answer becomes the normal one. If the record changes back first, the change closes by itself, with a follow-up alert.
        </li>
        <li>A change isn’t an outage: the monitor stays up and its uptime is unaffected. It goes to the same destinations as other alerts, and to PagerDuty as a warning.</li>
        <li>Past changes, and who accepted each one, are listed on the monitor’s page.</li>
      </ul>

      <h2 id="alerts">How problems are confirmed</h2>
      <p>
        A DNS problem is treated like a website outage: it opens an incident only after consecutive failed checks, and from most locations when a monitor is
        checked from several. <Link to="/docs/alerts">Routing by tag</Link>, alert delay, quiet hours and maintenance windows all apply, and a DNS monitor
        can appear on status pages and have an uptime badge. The domain it belongs to is also added to your <strong>Domains</strong> page, so you are warned
        before its registration lapses, up to your plan’s domain limit.
      </p>
      <p>
        Outage alerts name the value you expected, not the answer HawkEye received. Change alerts show the answers before and after, which HawkEye keeps
        for monitors with change alerts on.
      </p>

      <h2 id="limits">Good to know</h2>
      <ul>
        <li>
          <strong>Changes are not instant.</strong> HawkEye asks a DNS resolver, as your visitors’ devices do, so a change shows up once the
          old record expires from caches (its TTL). Planned changes can show as failing until you update the expected value, and send a change alert you
          can mark as intended.
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

export function StatusPageDomains() {
  return (
    <>
      <p>
        On the Agency plan, each status page can be served at an address on your client’s own domain, such as <code>status.yourclient.com</code>,
        instead of only at its HawkEye address. Visitors see your client’s name in the address bar, and HawkEye gets and renews the certificate for it.
        Each page can have one domain, and the page keeps working at its HawkEye address too.
      </p>

      <h2 id="set-up">Set it up</h2>
      <ol>
        <li>Open the status page on the <strong>Status pages</strong> page. Under <strong>Custom domain</strong>, enter the address, such as <code>status.yourclient.com</code>, and choose <strong>Set up domain</strong>.</li>
        <li>
          HawkEye shows two DNS records. Add both at the company that runs DNS for the domain (often the registrar, or a service such as Cloudflare):
          <ul>
            <li>a <strong>CNAME</strong> record from your address to <code>domains.hawkeye.observer</code>, which sends visitors to HawkEye;</li>
            <li>a <strong>TXT</strong> record at <code>_hawkeye.</code> followed by your address, holding a value that starts <code>hawkeye-verify=</code>, which proves the domain is yours.</li>
          </ul>
        </li>
        <li>
          Wait for HawkEye to see them. It checks every minute for the first hour, then every 15 minutes for three days, or choose <strong>Check now</strong>.
          When both records are right the domain shows as <strong>Live</strong> and the organization’s owner gets an email. The first visit takes a few
          seconds while the certificate is issued.
        </li>
      </ol>
      <p>Keep both records in place: HawkEye checks them every day, and the TXT record is what stops anyone else from using the address.</p>

      <h2 id="dns-tips">DNS tips</h2>
      <ul>
        <li><strong>Use a subdomain.</strong> A domain on its own, such as <code>yourclient.com</code>, can’t have a CNAME record, so it can’t be used.</li>
        <li>
          <strong>Names in your DNS provider.</strong> Many providers add the domain to a record’s name by themselves: if yours does, enter
          <code>status</code> and <code>_hawkeye.status</code> rather than the full names.
        </li>
        <li>
          <strong>Turn off proxying.</strong> If your provider offers a proxy, such as Cloudflare’s orange cloud, set the CNAME record to DNS only. HawkEye
          has to receive visitors directly to look after the certificate.
        </li>
        <li><strong>Changes take time.</strong> A new record can take up to an hour to be seen everywhere, longer if an old record with a long TTL is cached.</li>
      </ul>

      <h2 id="redirect">Send visitors to the domain</h2>
      <p>
        Once a domain is live, search engines are pointed to it, and the page still works at its HawkEye address. To send everyone to the domain, turn on
        <strong> Send visitors of /status/… to</strong> your domain in the page’s settings and save the page.
      </p>

      <h2 id="problems">If something changes</h2>
      <ul>
        <li>
          <strong>Needs attention</strong>: a record was changed or removed. The page is still served on the domain for 7 days while you fix it, and the
          owner is emailed with what is wrong.
        </li>
        <li>
          <strong>Stopped</strong>: the records stayed wrong for 7 days, so the page is no longer served on the domain. Put the records back and choose
          <strong> Check now</strong>.
        </li>
        <li>
          <strong>Removing a domain</strong> stops it at once, and you can delete its DNS records. If you move to a plan without custom domains, the domain
          is kept but not served until you move back.
        </li>
      </ul>

      <h2 id="what-is-served">What visitors see</h2>
      <p>
        Only the status page is served on your domain: no HawkEye sign-in, no other pages and no tracking. Monitored web addresses and error details are
        never shown, exactly as on the HawkEye address. With Agency you can also add your client’s logo, an accent color and a link back to their website,
        and hide “Powered by HawkEye”.
      </p>
    </>
  )
}

export function Alerts() {
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
        <li>
          <strong>PagerDuty</strong> through a service’s integration key: in PagerDuty, open the service, choose <strong>Integrations → Add an
          integration → Events API V2</strong>, and copy its Integration Key. An outage opens a PagerDuty incident (so whoever is on call is paged),
          and its recovery resolves that incident by itself. Slowdowns work the same way. Certificate and domain warnings open an incident that stays
          open until you resolve it in PagerDuty. When you add the key, HawkEye opens a test incident and resolves it at once.
        </li>
        <li><strong>Webhooks</strong>: a signed JSON request to your own endpoint, for anything else.</li>
      </ul>
      <p>Use <strong>Send test</strong> on the Alerts page to check that a destination works. A test alert says plainly that nothing is wrong.</p>

      <h2 id="what">What you are alerted about</h2>
      <ul>
        <li><strong>Outages and recoveries</strong> of websites and APIs, confirmed by consecutive failed checks (and by most locations, when a monitor is checked from several).
          See <a href="#recheck">re-check before alerting</a>.</li>
        <li><strong>Cron jobs</strong> that miss a ping or report a failure (<Link to="/docs/cron-job-monitoring">heartbeat monitors</Link>).</li>
        <li><strong>DNS records</strong> that disappear or stop matching the value you expect (<Link to="/docs/dns-monitoring">DNS monitors</Link>).</li>
        <li><strong>Slow responses</strong>, if you turn this on for a monitor: when responses stay above your threshold for several checks in a row.
          The alert says what got slower (the DNS lookup, connecting, the TLS handshake or the server’s wait) compared with the monitor’s past week.</li>
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

      <h2 id="recheck">Re-check before alerting</h2>
      <p>
        When a healthy monitor fails a check, HawkEye checks it again from the same location 15 seconds later instead of waiting for the next scheduled
        check. If that fails too, the outage is confirmed and you are alerted right away; if it passes, the failure is forgotten. A five-minute monitor
        on Free therefore alerts in seconds, not minutes, and two unrelated blips in a row no longer add up to an outage. With several locations, each
        failing location re-checks itself, and most locations must still agree.
      </p>
      <ul>
        <li>
          It is on by default, and only for requests that are safe to repeat: GET, HEAD and OPTIONS, DNS lookups, and multi-step checks whose steps
          all use those. A monitor that sends POST, PUT, PATCH or DELETE isn’t re-checked, so nothing that may change data is sent twice. Turn it on
          for such a monitor if repeating the request is safe (a search, for example).
        </li>
        <li>Turn it off, or on, with <strong>Re-check right away before alerting</strong> under Alert timing on the monitor’s edit page.</li>
        <li>A monitor that is already down isn’t re-checked, and 30-second checks aren’t either: the next check comes just as soon.</li>
        <li>Re-checks appear in the check history, labeled <strong>Re-check</strong>, but don’t count in uptime or response times.</li>
      </ul>

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

export function ApiWorkflows() {
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
        <li>For each step, give it a name, a method and an address. <strong>More options</strong> has headers, a body, authentication and checks on the response. Or choose <strong>Paste a cURL command</strong> on the step to fill it in from a command; a token or key in it is saved as a secret.</li>
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
      <p>
        Under the test results, open a step’s response to build the check from it. Click a field to add a check to that step, such as{' '}
        <code>status</code> equals <code>paid</code> or <code>count</code> is at least <code>1</code>, or to save its value for later steps, such as{' '}
        <code>data.token</code> saved as <code>token</code>. Secrets and saved values show as [hidden], and the responses aren’t kept.
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

export function ApiDocs() {
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
        <li><strong>Deploys only</strong> keys (the owner can make them) can only quiet alerts during a deploy and check your sites afterwards, for a CI pipeline. See <Link to="/docs/deploys">Deploys and CI</Link>.</li>
      </ul>
      <p>
        A key belongs to you and to the organization you made it in. It can do what you can do there and no more: if you are a member rather than the owner,
        owner-only actions are refused, and if you leave the organization your keys stop working.
      </p>

      <h2 id="requests">2. Send it with each request</h2>
      <p>Put the key in the <code>Authorization</code> header. Requests and responses are JSON.</p>
      <Code>{`curl https://hawkeye.observer/api/v1/monitors \\
  -H "Authorization: Bearer $HAWKEYE_API_KEY"`}</Code>
      <p>
        Pause a monitor and resume it later (needs a read and write key). For deploys, <Link to="/docs/deploys">Deploys and CI</Link> does this better:
        it quiets alerts with a key safe to keep in CI, and checks the site afterwards.
      </p>
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

export function ClaudeDocs() {
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
        Then ask “set up monitoring for this project”. Your code stays on your machine; HawkEye receives only the monitors you approve. The same plugin is
        available for <Link to="/docs/codex">Codex</Link>, and other coding agents that read skills can use the same{' '}
        <a href="https://github.com/EssenceOfChaos/hawkeye/tree/main/plugins/hawkeye">skill</a> with HawkEye’s MCP server configured as above.
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

export function CodexDocs() {
  return (
    <>
      <p>
        HawkEye’s plugin for Codex, OpenAI’s coding agent, connects Codex to your HawkEye account and sets up monitoring from your repository. Ask “set up
        monitoring for this project”, or ask about your monitoring as you work: “what’s down right now?” or “how was uptime for the shop this month?”
      </p>

      <h2 id="install">Install the plugin</h2>
      <p>In a terminal with the Codex CLI installed, run:</p>
      <Code>{`codex plugin marketplace add EssenceOfChaos/hawkeye
codex plugin add hawkeye@hawkeye
codex mcp login hawkeye`}</Code>
      <p>
        The last command opens HawkEye’s sign-in page in your browser. Sign in (with your two-factor code if you use one), choose the organization Codex may
        use, and choose <strong>Read only</strong>, or <strong>Read and make changes</strong> to let Codex make changes too (on Free, only adding monitors).
        Then start a new Codex session. <code>codex mcp list</code> shows whether you’re signed in.
      </p>
      <p>
        Signing in uses port 47831 on your computer to hand the sign-in back to Codex, so it must be free while you sign in. There is no key to copy: you can
        disconnect Codex at any time from <strong>Connected apps</strong> on HawkEye’s API keys page.
      </p>

      <h2 id="setup">Set up monitoring from your code</h2>
      <p>
        Ask Codex to “set up monitoring for this project”. It finds your production domain, your cron jobs and scheduled tasks (crontab, GitHub Actions
        schedules, Kubernetes CronJobs, platform crons and in-app schedulers) and your health endpoints, asks HawkEye to suggest monitors for your live site,
        and shows you one plan. Once you approve it, Codex creates the monitors, including a heartbeat for each job, and adds the ping to the end of each
        job. Ping addresses are read from environment variables or CI secrets, never written into your code, and Codex tells you which to set.
      </p>
      <p>Nothing is created and no file is changed before you approve. Your code stays on your machine; HawkEye receives only the monitors you approve.</p>

      <h2 id="api-key">Connect with an API key instead</h2>
      <p>
        To connect without signing in, for example on a server, create an <Link to="/docs/api">API key</Link> and add HawkEye’s MCP server to{' '}
        <code>~/.codex/config.toml</code>, with the key in the <code>HAWKEYE_API_KEY</code> environment variable:
      </p>
      <Code>{`[mcp_servers.hawkeye]
url = "https://hawkeye.observer/mcp"
bearer_token_env_var = "HAWKEYE_API_KEY"`}</Code>

      <h2 id="tools">What Codex can do</h2>
      <p>
        The same as Claude: read your monitors, uptime, incidents, status pages and maintenance windows, suggest monitors for a domain, add monitors, and
        with the right plan and role pause monitors, set up multi-step API checks, post incident updates and schedule maintenance. See{' '}
        <Link to="/docs/claude#tools">what an assistant can do</Link> for the details.
      </p>
      <p>
        Codex acts as you, in the organization you chose, with your permissions, and no more. It can’t see or change your password, billing, team, alert
        destinations or API keys, and every change it makes is in the organization’s security log.
      </p>
    </>
  )
}

export function DeploysDocs() {
  return (
    <>
      <p>
        When you deploy your website or API, it restarts, and HawkEye would see it fail for a moment and alert you about something you did on purpose.
        Two lines in your CI pipeline fix that: one before the deploy quiets the alerts, one after it ends the quiet period and checks your sites right
        away, so a broken deploy fails the pipeline (and can roll back) instead of reaching your customers.
      </p>

      <h2 id="key">1. Create a deploy key</h2>
      <p>
        Open <strong>API keys</strong> from the account menu and create a key with <strong>Deploys only</strong> access (only the organization’s owner
        can). A deploy key can start and finish deploys and nothing else, not even read your monitors, so it’s safe to store as a CI secret. Save it as
        a secret named <code>HAWKEYE_DEPLOY_KEY</code>.
      </p>

      <h2 id="curl">2. Add two lines to your pipeline</h2>
      <p>Before deploying, quiet alerts for the monitors the deploy affects (here, the ones tagged <code>production</code>):</p>
      <Code>{`curl -fsS -X POST https://hawkeye.observer/api/v1/deploys \\
  -H "Authorization: Bearer $HAWKEYE_DEPLOY_KEY" \\
  -d tag=production -d note="$GIT_COMMIT"`}</Code>
      <p>
        After deploying, end the quiet period and check those monitors. If one is failing, HawkEye answers with an error and says which monitor failed
        and why, so the step fails. The retries give a freshly started site about a minute to come up:
      </p>
      <Code>{`curl -sS --fail-with-body --retry 5 --retry-delay 10 --retry-all-errors \\
  -X POST https://hawkeye.observer/api/v1/deploys/latest/finish \\
  -H "Authorization: Bearer $HAWKEYE_DEPLOY_KEY"`}</Code>
      <p>
        <code>latest</code> means the newest deploy started with the same key in the last day, so the two steps don’t need to pass anything between them.
        Leave out <code>tag</code> to cover every monitor.
      </p>

      <h2 id="github">GitHub Actions</h2>
      <p>HawkEye’s action wraps the same two calls and writes the result to the job summary:</p>
      <Code>{`- uses: EssenceOfChaos/hawkeye/actions/deploy@deploy-action-v1
  with:
    key: \${{ secrets.HAWKEYE_DEPLOY_KEY }}
    step: start
    tag: production

- run: ./deploy.sh

- uses: EssenceOfChaos/hawkeye/actions/deploy@deploy-action-v1
  with:
    key: \${{ secrets.HAWKEYE_DEPLOY_KEY }}
    step: finish`}</Code>

      <h2 id="gitlab">GitLab CI</h2>
      <p>Each <code>&gt;</code> block is one command, folded over several lines:</p>
      <Code>{`deploy:
  script:
    - >
      curl -fsS -X POST https://hawkeye.observer/api/v1/deploys
      -H "Authorization: Bearer $HAWKEYE_DEPLOY_KEY"
      -d tag=production -d note="$CI_COMMIT_SHORT_SHA"
    - ./deploy.sh
    - >
      curl -sS --fail-with-body --retry 5 --retry-delay 10 --retry-all-errors
      -X POST https://hawkeye.observer/api/v1/deploys/latest/finish
      -H "Authorization: Bearer $HAWKEYE_DEPLOY_KEY"`}</Code>

      <h2 id="options">Options</h2>
      <ul>
        <li><code>tag</code>: the monitors with this tag. Leave it out for every monitor.</li>
        <li><code>minutes</code>: when alerts come back on by themselves if the pipeline never finishes (1 to 120, default 15).</li>
        <li><code>note</code>: a commit, version or pipeline link, shown under <strong>Maintenance</strong> and in the security log.</li>
        <li><code>?verify=false</code> on finish: just end the quiet period, without a check.</li>
      </ul>

      <h2 id="details">Good to know</h2>
      <ul>
        <li>
          While a deploy is open, HawkEye keeps checking, but those monitors don’t open incidents or send alerts, and the time doesn’t count against
          uptime. Your public status pages don’t show it as maintenance.
        </li>
        <li>
          Starting a deploy again for the same monitors (a retried pipeline step) extends it rather than opening a second one. Alerts are always back on
          after <code>minutes</code>, even if the pipeline crashed before finishing.
        </li>
        <li>
          The check after a deploy covers up to 10 monitors (heartbeats and paused monitors are skipped) and allows about 20 seconds; a failing monitor is
          tried once more. Its results aren’t added to the check history: once alerts are back on, the regular checks decide whether there’s an outage.
        </li>
        <li>
          Quieting alerts is part of maintenance windows, on Pro and Agency. The check on its own works on every plan, including Free:{' '}
          <code>POST /api/v1/deploys/check</code> with the same <code>tag</code>.
        </li>
        <li>Recent deploys are listed under <strong>Maintenance</strong> for 30 days, and every start and finish is in the security log.</li>
      </ul>
    </>
  )
}

