import { useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type SiteReport } from '../../api'
import { trackEvent } from '../../analytics'
import { Alert } from '../../components/Alert'
import { Field } from '../../components/Field'
import { ToolShell } from '../../components/ToolShell'
import { statusLabel } from '../../tools/httpStatus'
import { useToolUse } from '../../tools/useToolTracking'

type Look = { icon: string; tone: 'up' | 'warn' | 'down'; headline: (host: string) => string; detail: (r: SiteReport) => React.ReactNode }

const code = (r: SiteReport) => (r.status_code ? statusLabel(r.status_code) : '')

const LOOKS: Record<SiteReport['reason'], Look> = {
  ok: {
    icon: '✔', tone: 'up', headline: (h) => `${h} is up`,
    detail: (r) => `It answered with HTTP ${code(r)}${r.response_time_ms !== undefined ? ` in ${r.response_time_ms.toLocaleString()} ms` : ''}.`,
  },
  http_4xx: {
    icon: '⚠', tone: 'warn', headline: (h) => `${h} is reachable, but it returned an error`,
    detail: (r) => (
      <>
        The server answered with HTTP {code(r)}, so it is running. {r.status_code === 403 || r.status_code === 429 || r.status_code === 401
          ? 'Many sites refuse automated requests with this code (to block bots), so visitors in a browser may be fine.'
          : r.status_code === 404 ? 'The home page itself was not found, which usually means a configuration problem on the site.' : 'This request was refused, which does not always mean visitors are affected.'}
      </>
    ),
  },
  http_5xx: {
    icon: '✖', tone: 'down', headline: (h) => `${h} is down`,
    detail: (r) => `The server answered with HTTP ${code(r)}. It is running, but it is failing to serve the page, so visitors will see an error too.`,
  },
  dns: {
    icon: '✖', tone: 'down', headline: (h) => `We could not find ${h}`,
    detail: () => 'The domain name did not resolve, so no browser can reach it either. Check the spelling. If it is your domain, check that it has not expired and that its DNS records exist.',
  },
  connect: {
    icon: '✖', tone: 'down', headline: (h) => `${h} is not accepting connections`,
    detail: () => 'The name resolves, but the server did not accept a connection on the standard web ports. The server may be offline, restarting, or blocking connections.',
  },
  tls: {
    icon: '✖', tone: 'down', headline: (h) => `${h} has a problem with its secure connection`,
    detail: () => (
      <>
        The server answered, but the secure (HTTPS) connection failed, usually because of a certificate problem. Browsers would show visitors a security warning.{' '}
        <Link to="/tools/ssl-certificate-checker">Check the certificate</Link> to see what is wrong.
      </>
    ),
  },
  timeout: {
    icon: '✖', tone: 'down', headline: (h) => `${h} did not answer in time`,
    detail: () => 'The server accepted the connection or the request but did not respond within about six seconds. It may be overloaded or stuck.',
  },
  redirect: {
    icon: '✖', tone: 'down', headline: (h) => `${h} redirects in a loop`,
    detail: () => 'The home page sends visitors from address to address too many times without arriving anywhere. Browsers stop with a "too many redirects" error.',
  },
  other: {
    icon: '✖', tone: 'down', headline: (h) => `${h} could not be checked`,
    detail: () => 'The request failed in a way we could not classify. Try again in a moment.',
  },
}

const SPEED: Record<NonNullable<SiteReport['speed']>, string> = { fast: 'Fast', moderate: 'Moderate', slow: 'Slow' }

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function Result({ r }: { r: SiteReport }) {
  const look = LOOKS[r.reason]
  return (
    <section className="card tool-report" aria-labelledby="site-result-h">
      <div className={`tool-status site-${look.tone}`}>
        <span aria-hidden="true">{look.icon}</span>
        <div>
          <h2 id="site-result-h">{look.headline(r.host)}</h2>
          <p>{look.detail(r)}</p>
        </div>
      </div>
      {r.https_error && (
        <p className="tool-warn">
          The secure (HTTPS) version could not be reached ({r.https_error === 'tls' ? 'a TLS or certificate problem' : 'it did not accept connections'}), but plain HTTP answered. Visitors
          using HTTPS will not get through.
        </p>
      )}
      <dl className="tool-details">
        {r.status_code !== undefined && <Row label="Status code">{statusLabel(r.status_code)}</Row>}
        {r.response_time_ms !== undefined && (
          <Row label="Response time">
            <span className="mono">{r.response_time_ms.toLocaleString()} ms</span>
            {r.speed && <span className={`speed speed-${r.speed}`}> · {SPEED[r.speed]}</span>}
          </Row>
        )}
        <Row label="Address checked"><span className="mono">{r.url}</span></Row>
        {r.final_url && <Row label="Ended up at"><span className="mono">{r.final_url}</span></Row>}
        <Row label="Checked">{new Date(r.checked_at).toLocaleString()}</Row>
      </dl>
      {r.speed && (
        <p className="hint">
          Speed is judged against Google’s guidance for time to first byte (good up to 0.8 s, poor above 1.8 s). This is one measurement from one location and includes
          redirects, DNS, connecting and TLS, so it varies from run to run. Results are reused for up to 30 seconds.
        </p>
      )}
    </section>
  )
}

export function WebsiteStatusPage() {
  const used = useToolUse('is-website-down')
  const [host, setHost] = useState('')
  const [busy, setBusy] = useState(false)
  const [report, setReport] = useState<SiteReport | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (busy) return
    used()
    setBusy(true)
    setError(null)
    try {
      const r = await api<SiteReport>('POST', '/tools/site-check', { host })
      setReport(r)
      trackEvent('tool_result', { tool: 'is-website-down', status: r.verdict })
    } catch (err) {
      setReport(null)
      setError(err instanceof ApiError ? err.message : 'We could not reach the checker. Check your connection and try again.')
    } finally {
      setBusy(false)
    }
  }

  const bad = report !== null && report.verdict !== 'up'
  return (
    <ToolShell
      tool="is-website-down"
      title="Is My Website Down?"
      intro="Check whether a website is up right now. Enter a domain to see whether it answers, its HTTP status code and how long it takes to respond."
      cta={{
        heading: bad ? 'Be the first to know next time' : 'Do not wait for a customer to tell you',
        text: 'HawkEye checks your website or API on a schedule and alerts you by email, Slack or webhook as soon as an outage is confirmed, then tells you when it is back.',
      }}
    >
      <form className="card" onSubmit={onSubmit} noValidate aria-labelledby="site-form-h">
        <h2 id="site-form-h">Check a website</h2>
        <Field
          label="Website"
          type="text"
          inputMode="url"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          placeholder="example.com"
          value={host}
          onChange={(e) => setHost(e.target.value)}
          hint="A domain name, for example example.com. We check its home page."
        />
        <button type="submit" disabled={busy || host.trim() === ''}>{busy ? 'Checking…' : 'Check website'}</button>
      </form>

      <div aria-live="polite">
        {busy && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Contacting the website…</p>}
        {error && <Alert>{error}</Alert>}
        {report && !busy && <Result r={report} />}
      </div>

      <section aria-labelledby="site-help-h">
        <h2 id="site-help-h">Down for everyone, or just you?</h2>
        <p>
          This checks from our server, so it shows whether the website answers from outside your network. If it is <strong>up here but not for you</strong>, the problem is
          probably on your side. Things to try:
        </p>
        <ul>
          <li>Reload the page with the cache bypassed (Ctrl+Shift+R, or Cmd+Shift+R on a Mac), or try a private window.</li>
          <li>Try another network, such as your phone’s mobile data. Your router, ISP or a VPN may be blocking the site.</li>
          <li>Restart your browser or flush your computer’s DNS cache; an old DNS answer can send you to the wrong place.</li>
          <li>Check your computer’s date and time: a wrong clock makes secure sites fail.</li>
        </ul>
        <p>
          If it is <strong>down here too</strong> and the site is yours, look at your hosting provider’s status page, your server’s load and logs, and whether your domain
          and certificate are still valid.
        </p>
        <details className="explain">
          <summary>What do the status codes mean?</summary>
          <p>
            <strong>2xx</strong> and <strong>3xx</strong> (for example 200 OK, 301 redirect) mean the site answered normally. <strong>4xx</strong> means the request was
            refused or the page was not found: 404 is a missing page, and 403 or 429 often mean an automated visitor was turned away. <strong>5xx</strong> means the server
            itself failed: 500 is a general error, 502 and 504 mean a gateway could not get an answer from the server behind it, and 503 means it is overloaded or in maintenance.
          </p>
        </details>
        <details className="explain">
          <summary>How does this check work?</summary>
          <p>
            We send one GET request for the home page of the domain you enter (any path or query after the domain is ignored), follow up to five redirects, and note the
            status code and how long the response took. We do not keep the page, and we do not save a record of the domains people check (a domain is only held in memory for a few minutes to limit repeat checks). Requests identify themselves as{' '}
            <code>HawkEye-StatusChecker/1.0</code>. We never check private or internal addresses, and only use the standard web ports (80 and 443).
          </p>
        </details>
      </section>
    </ToolShell>
  )
}
