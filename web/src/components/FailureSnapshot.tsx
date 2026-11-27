import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type FailureSnapshot as Snapshot, type ResponseFacts, type SnapshotChange } from '../api'
import { TimingBar } from './TimingBar'

const PHASE: Record<string, string> = { dns: 'DNS lookup', connect: 'Connect', tls: 'TLS handshake', wait: 'Server wait' }
const FIELD: Record<string, string> = {
  status: 'Status', server_addr: 'Server address', final_url: 'Address', content_type: 'Content type', certificate: 'Certificate', size: 'Size',
}

function bytes(n: number): string {
  if (n < 1000) return `${n} B`
  if (n < 10_000) return `${(n / 1000).toFixed(1)} KB`
  if (n < 1_000_000) return `${Math.floor(n / 1000)} KB`
  return `${(n / 1_000_000).toFixed(1)} MB`
}

function response(f: ResponseFacts): string | null {
  if (!f.status_code) return null
  const parts = [`HTTP ${f.status_code}`]
  if (f.content_type) parts.push(f.content_type)
  if (f.size_bytes >= 0) parts.push(`${f.size_at_least ? 'at least ' : ''}${bytes(f.size_bytes)}`)
  return parts.join(' · ')
}

function Change({ c }: { c: SnapshotChange }) {
  if (c.field === 'timing') {
    return <li><strong>{PHASE[c.name ?? ''] ?? 'Server wait'}</strong>: <span className="mono">{c.after}</span> <span className="hint">(usually {c.before})</span></li>
  }
  const label = c.field === 'header' ? c.name : FIELD[c.field] ?? c.field
  return <li><strong>{label}</strong>: <span className="mono">{c.before}</span> → <span className="mono">{c.after}</span></li>
}

/**
 * What HawkEye saw when an incident's outage was confirmed: the response, what changed since the last good check, each location, the timings
 * and the allowlisted headers, plus the opt-in body excerpt behind a button. Renders nothing for incidents without a snapshot (heartbeats, DNS
 * monitors, older incidents). Response text and header values come from the monitored site, so they are only ever shown as text.
 */
export function FailureSnapshot({ incidentID, monitorID }: { incidentID: string; monitorID: string }) {
  const [snap, setSnap] = useState<Snapshot | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [body, setBody] = useState<{ text: string; truncated: boolean } | null>(null)
  const [bodyError, setBodyError] = useState<string | null>(null)
  const [loadingBody, setLoadingBody] = useState(false)

  useEffect(() => {
    let live = true
    api<{ snapshot: Snapshot }>('GET', `/incidents/${incidentID}/snapshot`)
      .then((r) => { if (live) setSnap(r.snapshot) })
      .catch((e: unknown) => {
        if (!live) return
        if (e instanceof ApiError && e.status === 404) setSnap(null)
        else setError(e instanceof ApiError ? e.message : 'Could not load what HawkEye saw.')
      })
    return () => { live = false }
  }, [incidentID])

  async function showBody() {
    setLoadingBody(true)
    setBodyError(null)
    try {
      setBody((await api<{ body: { text: string; truncated: boolean } }>('GET', `/incidents/${incidentID}/snapshot/body`)).body)
    } catch (e) {
      setBodyError(e instanceof ApiError ? e.message : 'Could not load the response.')
    } finally {
      setLoadingBody(false)
    }
  }

  if (error) return <p className="error">{error}</p>
  if (snap === undefined) return <p className="hint" role="status">Loading what HawkEye saw…</p>
  if (snap === null) return null

  const f = snap.facts
  const t = f.timings
  const resp = response(f)
  const headers = f.headers ?? []
  return (
    <section className="failure-snapshot" aria-labelledby={`snapshot-${incidentID}`}>
      <h3 id={`snapshot-${incidentID}`}>What HawkEye saw</h3>
      <p className="hint">
        The check that confirmed the outage
        {/* Only monitors checked from several locations have names for them; with one, where it ran says nothing. */}
        {snap.locations.length > 1 && <>, from {snap.locations.find((l) => l.region === snap.region)?.name ?? snap.region}</>},{' '}
        <time dateTime={snap.checked_at}>{new Date(snap.checked_at).toLocaleString()}</time>.
      </p>
      <dl className="dns-diff">
        {snap.step_name && <><dt>Failed step</dt><dd>{snap.step_index !== null && `${snap.step_index + 1}. `}{snap.step_name}</dd></>}
        <dt>Response</dt><dd className="mono">{resp ?? 'No response arrived'}</dd>
        {f.final_url && <><dt>Ended at</dt><dd className="mono">{f.final_url}</dd></>}
        {f.server_addr && <><dt>Server address</dt><dd className="mono">{f.server_addr}</dd></>}
        {f.cert_expires_at && (
          <><dt>Certificate</dt><dd>Expires {new Date(f.cert_expires_at).toLocaleDateString()}{f.cert_issuer && <>, issued by {f.cert_issuer}</>}</dd></>
        )}
      </dl>

      {snap.changes.length > 0 && (
        <>
          <h4>Changed since the last good check</h4>
          <ul className="snapshot-changes">{snap.changes.map((c, i) => <Change key={i} c={c} />)}</ul>
        </>
      )}

      {snap.locations.length > 1 && (
        <>
          <h4>Each location</h4>
          <ul className="snapshot-changes">
            {snap.locations.map((l) => (
              <li key={l.region}>
                <strong>{l.name || l.region}</strong>:{' '}
                {l.status === 'down' ? <span className="down-text">{l.error || 'failing'}</span> : l.status === 'up' ? 'OK' : 'no result yet'}
              </li>
            ))}
          </ul>
        </>
      )}

      {t && (
        <TimingBarForCheck dns={t.dns_ms} connect={t.connect_ms} tls={t.tls_ms} wait={t.wait_ms} redirects={t.redirects} />
      )}

      {headers.length > 0 && (
        <details className="table-view">
          <summary>Response headers ({headers.length})</summary>
          <div className="table-wrap">
            <table>
              <caption className="sr-only">Response headers kept for this incident</caption>
              <thead><tr><th>Header</th><th>Value</th></tr></thead>
              <tbody>{headers.map((h) => <tr key={h.name}><td className="mono">{h.name}</td><td className="mono wrap-anywhere">{h.value}</td></tr>)}</tbody>
            </table>
          </div>
          <p className="hint">Only a fixed list of headers is kept, so cookies and credentials never are.</p>
        </details>
      )}

      {snap.has_body_excerpt && !body && (
        <p>
          <button type="button" onClick={() => void showBody()} disabled={loadingBody}>{loadingBody ? 'Loading…' : 'Show response'}</button>{' '}
          <span className="hint">The first 4 KB of the failed response, kept for 30 days after the incident ends.</span>
        </p>
      )}
      {bodyError && <p className="error">{bodyError}</p>}
      {body && (
        <>
          <pre className="test-body" aria-label="Failed response body">{body.text}</pre>
          {body.truncated && <p className="hint">Only the first 4 KB of the response was kept.</p>}
        </>
      )}
      {!snap.has_body_excerpt && snap.body_excerpt_cleared_at && (
        <p className="hint">The response was removed 30 days after the incident ended.</p>
      )}
      {!snap.has_body_excerpt && !snap.body_excerpt_cleared_at && f.status_code ? (
        <p className="hint">
          To see the response itself next time, turn on <Link to={`/monitors/${monitorID}/edit`}>Keep the start of failed responses</Link> in the
          monitor’s alert settings.
        </p>
      ) : null}
    </section>
  )
}

function TimingBarForCheck({ dns, connect, tls, wait, redirects }: { dns: number; connect: number; tls: number; wait: number; redirects: number }) {
  return (
    <TimingBar title="Where the time went" note={redirects > 0 ? `this check, ${redirects} redirect${redirects === 1 ? '' : 's'}` : 'this check'}
      timings={{ dns_ms: dns, connect_ms: connect, tls_ms: tls, wait_ms: wait, other_ms: 0, checks: 1 }} />
  )
}
