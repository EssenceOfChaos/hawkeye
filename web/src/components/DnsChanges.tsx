import { useState } from 'react'
import { api, ApiError, type DnsChange, type Monitor } from '../api'
import { formatShortDateTime } from '../format'
import { useApi } from '../hooks'
import { Alert } from './Alert'

const answers = (a: string[]) => (a.length === 0 ? '(none)' : a.join(', '))

/**
 * A DNS monitor's change alerts (docs/plan-dns-change-alerts.md): while a change is open, a warning with the record before and after and a button
 * to accept it as intended; then the record's current answers and its past changes. Any member can accept a change (members can edit monitors);
 * the security log records who did. onChanged lets the page reload the monitor, whose badge shows an open change.
 */
export function DnsChanges({ monitor, onChanged }: { monitor: Monitor; onChanged: () => void }) {
  const [reload, setReload] = useState(0)
  const data = useApi<{ dns_changes: DnsChange[]; current: string[] | null }>(`/monitors/${monitor.id}/dns-changes?r=${reload}`)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!monitor.dns) return null
  const list = data.data?.dns_changes ?? []
  const open = list.find((c) => c.status === 'open')
  const past = list.filter((c) => c.status !== 'open')

  async function accept(c: DnsChange) {
    setBusy(true)
    setError(null)
    try {
      await api('POST', `/monitors/${monitor.id}/dns-changes/${c.id}/accept`)
      setReload((n) => n + 1)
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server. Try again.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      {open && (
        <Alert tone="warning" role="status">
          <strong>{open.record_type} record changed</strong> · <time dateTime={open.detected_at}>{formatShortDateTime(open.detected_at)}</time>
          <dl className="dns-diff">
            <dt>Before</dt><dd className="mono">{answers(open.before)}</dd>
            <dt>After</dt><dd className="mono">{answers(open.after)}</dd>
          </dl>
          <div className="row">
            <button type="button" onClick={() => void accept(open)} disabled={busy}>This change was intended</button>
            <span className="hint">If it wasn’t you, check your domain registrar and DNS provider accounts now.</span>
          </div>
          {error && <p className="error">{error}</p>}
        </Alert>
      )}
      {monitor.dns.change_alerts !== false && (
        <section aria-labelledby="dns-changes-h">
          <h2 id="dns-changes-h">DNS changes</h2>
          {data.data?.current && (
            <p>Current answer: <span className="mono">{answers(data.data.current)}</span></p>
          )}
          {data.data && !data.data.current && <p className="hint">HawkEye remembers the record’s answer after the next lookup.</p>}
          {past.length === 0 && data.data && <p className="hint">No changes yet. You’ll be alerted when the record changes.</p>}
          {past.length > 0 && (
            <ul className="plain-list">
              {past.map((c) => (
                <li key={c.id}>
                  <time dateTime={c.detected_at}>{formatShortDateTime(c.detected_at)}</time>{' '}
                  <span className="mono">{c.summary}</span>{' '}
                  <span className="hint">
                    · {c.status === 'accepted' ? `Accepted${c.accepted_by ? ` by ${c.accepted_by}` : ''}` : 'Changed back'}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </>
  )
}
