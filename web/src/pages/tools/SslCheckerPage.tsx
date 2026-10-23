import { useState } from 'react'
import { api, ApiError, type SslReport } from '../../api'
import { trackEvent } from '../../analytics'
import { Alert } from '../../components/Alert'
import { Field } from '../../components/Field'
import { ToolShell } from '../../components/ToolShell'
import { useToolUse } from '../../tools/useToolTracking'

const HEADLINE: Record<SslReport['status'], string> = {
  valid: 'Certificate is valid',
  expiring: 'Certificate expires soon',
  expired: 'Certificate has expired',
  invalid: 'Certificate has a problem',
}

const date = (iso: string) => new Date(iso).toLocaleDateString('en-US', { year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC' })

function daysText(days: number): string {
  if (days < 0) return `Expired ${-days} day${days === -1 ? '' : 's'} ago`
  return `${days} day${days === 1 ? '' : 's'} left`
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function Report({ r }: { r: SslReport }) {
  const c = r.certificate
  const errors = r.problems.filter((p) => p.severity === 'error')
  const warnings = r.problems.filter((p) => p.severity === 'warning')
  return (
    <section className="card tool-report" aria-labelledby="ssl-result-h">
      <div className={`tool-status ssl-${r.status}`}>
        <span aria-hidden="true">{r.status === 'valid' ? '✔' : r.status === 'expiring' ? '⚠' : '✖'}</span>
        <div>
          <h2 id="ssl-result-h">{HEADLINE[r.status]}</h2>
          <p className="mono">{r.host} · {daysText(r.days_remaining)}</p>
        </div>
      </div>

      {r.problems.length > 0 && (
        <ul className="ssl-problems">
          {[...errors, ...warnings].map((p) => (
            <li key={p.code} className={p.severity === 'error' ? 'tool-fail' : 'tool-warn'}>
              <span className="sr-only">{p.severity === 'error' ? 'Problem: ' : 'Warning: '}</span>
              {p.message}
            </li>
          ))}
        </ul>
      )}

      <dl className="tool-details">
        <Row label="Issued to">{c.subject_name}{c.subject_org && <span className="hint"> · {c.subject_org}</span>}</Row>
        <Row label="Issued by">{c.issuer_name}{c.issuer_org && c.issuer_org !== c.issuer_name && <span className="hint"> · {c.issuer_org}</span>}</Row>
        <Row label="Valid from">{date(c.not_before)}</Row>
        <Row label="Valid until">{date(c.not_after)}</Row>
        <Row label="Trusted by browsers">{r.trusted ? 'Yes' : 'No'}</Row>
        <Row label="Name matches">{r.hostname_match ? 'Yes' : 'No'}</Row>
        <Row label="Covers">
          <span className="mono">{c.dns_names.join(', ') || '—'}</span>
          {c.dns_names_truncated && <span className="hint"> and more</span>}
        </Row>
        <Row label="Public key">{c.key_algorithm}{c.key_bits ? ` ${c.key_bits}-bit` : ''}</Row>
        <Row label="Signature">{c.signature_algorithm}</Row>
        {r.tls_version && <Row label="Connection">{r.tls_version}, {r.cipher_suite}</Row>}
        <Row label="SHA-256 fingerprint"><span className="mono ssl-fp">{c.sha256_fingerprint.match(/.{2}/g)?.join(':')}</span></Row>
        <Row label="Serial number"><span className="mono ssl-fp">{c.serial_number}</span></Row>
      </dl>

      {r.chain.length > 0 && (
        <details className="explain">
          <summary>Certificate chain ({r.chain.length})</summary>
          <ol className="ssl-chain">
            {r.chain.map((l, i) => (
              <li key={i}>
                <strong>{l.subject_name}</strong>
                <span className="hint"> issued by {l.issuer_name}, valid until {date(l.not_after)}</span>
              </li>
            ))}
          </ol>
        </details>
      )}
      <p className="hint">Checked {new Date(r.checked_at).toLocaleString()}. Results may be up to a minute old.</p>
    </section>
  )
}

export function SslCheckerPage() {
  const used = useToolUse('ssl-certificate-checker')
  const [host, setHost] = useState('')
  const [busy, setBusy] = useState(false)
  const [report, setReport] = useState<SslReport | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (busy) return
    used()
    setBusy(true)
    setError(null)
    try {
      const r = await api<SslReport>('POST', '/tools/ssl-check', { host })
      setReport(r)
      trackEvent('tool_result', { tool: 'ssl-certificate-checker', status: r.status })
    } catch (err) {
      setReport(null)
      setError(err instanceof ApiError ? err.message : 'We could not reach the checker. Check your connection and try again.')
    } finally {
      setBusy(false)
    }
  }

  const needsAttention = report !== null && report.status !== 'valid'
  return (
    <ToolShell
      tool="ssl-certificate-checker"
      title="SSL Certificate Checker"
      intro="Enter a domain to see when its SSL certificate expires, who issued it, and whether browsers will trust it."
      cta={{
        heading: needsAttention ? 'Do not be caught out by the next expiry' : 'Get warned before it expires',
        text: 'HawkEye emails you 30, 14, 7 and 1 days before an HTTPS certificate expires, on every plan, and tells you if the site itself goes down. Add the address once and it keeps watching.',
      }}
    >
      <form className="card" onSubmit={onSubmit} noValidate aria-labelledby="ssl-form-h">
        <h2 id="ssl-form-h">Check a certificate</h2>
        <Field
          label="Domain name"
          type="text"
          inputMode="url"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          placeholder="example.com"
          value={host}
          onChange={(e) => setHost(e.target.value)}
          hint="A public website, for example example.com. We connect on the standard HTTPS port (443)."
        />
        <button type="submit" disabled={busy || host.trim() === ''}>{busy ? 'Checking…' : 'Check certificate'}</button>
      </form>

      <div aria-live="polite">
        {busy && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Connecting to the server…</p>}
        {error && <Alert>{error}</Alert>}
        {report && !busy && <Report r={report} />}
      </div>

      <section aria-labelledby="ssl-what-h">
        <h2 id="ssl-what-h">What this checks</h2>
        <p>
          The checker opens a secure connection to the domain on port 443, reads the certificate the server presents, and checks it the way a browser would:
          that it is within its dates, that it was issued for this name, and that its chain leads to an authority browsers trust. It reads the certificate and
          disconnects; it does not load the website or look at its pages.
        </p>
        <details className="explain">
          <summary>What do the results mean?</summary>
          <p>
            <strong>Valid</strong> means more than 30 days remain and nothing is wrong. <strong>Expires soon</strong> means 30 days or fewer remain: renew it now.
            <strong> Expired</strong> means browsers show a full-page warning to every visitor. <strong>Has a problem</strong> means the certificate is in date but
            browsers would still object: it was issued for a different name, comes from an authority browsers do not trust, is self-signed, or is not valid yet.
          </p>
        </details>
        <details className="explain">
          <summary>Why do certificates expire so often now?</summary>
          <p>
            Publicly trusted certificates are being made shorter-lived. Since March 15, 2026 they can last at most 200 days, and the maximum falls to 47 days
            in 2029. Most sites renew automatically, but automatic renewal can fail silently, so watching the expiry date is still worth doing.
          </p>
        </details>
        <details className="explain">
          <summary>The checker says it cannot connect or find the domain</summary>
          <p>
            Check the spelling first. We can only check public websites that answer on port 443 with a TLS certificate. A site that is down, only serves plain
            HTTP, or is on a private network cannot be checked here. If the domain was only just set up, its DNS records may not have spread yet.
          </p>
        </details>
      </section>
    </ToolShell>
  )
}
