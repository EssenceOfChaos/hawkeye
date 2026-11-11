import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import { Alert } from './Alert'
import { categoryNames, describe, device, who, type LogEvent, type LogPage } from '../securityLog'

const when = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

/**
 * A security log, newest first, with a category filter and "Show older". `path` is the organization's log (/security-log, owners only) or the
 * person's own (/account/security-log).
 */
export function SecurityLog({ path, pageSize = 50, caption }: { path: string; pageSize?: number; caption: string }) {
  const [events, setEvents] = useState<LogEvent[] | null>(null)
  const [next, setNext] = useState<number | null>(null)
  const [categories, setCategories] = useState<string[]>([])
  const [category, setCategory] = useState('')
  const [days, setDays] = useState(365)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(
    async (before: number | null) => {
      setBusy(true)
      try {
        const q = new URLSearchParams({ limit: String(pageSize) })
        if (before) q.set('before', String(before))
        if (category) q.set('category', category)
        const r = await api<LogPage>('GET', `${path}?${q}`)
        setEvents((prev) => (before && prev ? [...prev, ...r.events] : r.events))
        setNext(r.next_before)
        setCategories(r.categories)
        setDays(r.retention_days)
        setError(null)
      } catch (e) {
        setError(e instanceof ApiError ? e.message : 'Could not load the security log.')
      } finally {
        setBusy(false)
      }
    },
    [path, pageSize, category],
  )

  useEffect(() => {
    void load(null)
  }, [load])

  const filterId = `${path.replaceAll('/', '-')}-category`
  return (
    <>
      {categories.length > 1 && (
        <div className="field narrow-left">
          <label htmlFor={filterId}>Show</label>
          <select id={filterId} value={category} onChange={(e) => setCategory(e.target.value)}>
            <option value="">Everything</option>
            {categories.map((c) => (
              <option key={c} value={c}>{categoryNames[c] ?? c}</option>
            ))}
          </select>
        </div>
      )}
      {error && <Alert>{error}</Alert>}
      {events === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}
      {events && events.length === 0 && <p className="hint">Nothing here yet.</p>}
      {events && events.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">{caption}</caption>
            <thead>
              <tr><th>When</th><th>Who</th><th>What</th><th>From</th></tr>
            </thead>
            <tbody>
              {events.map((e) => {
                const w = who(e)
                const d = device(e.user_agent)
                return (
                  <tr key={e.id}>
                    <td><time dateTime={e.occurred_at}>{when(e.occurred_at)}</time></td>
                    <td>{w.label}{w.note && <div className="hint">{w.note}</div>}</td>
                    <td className={e.action === 'account.sign_in_failed' ? 'error' : undefined}>{describe(e)}</td>
                    <td>
                      {e.ip ? <code>{e.ip}</code> : <span className="hint">Not recorded</span>}
                      {d && <div className="hint" title={e.user_agent ?? undefined}>{d}</div>}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {next && (
        <button type="button" disabled={busy} onClick={() => void load(next)}>
          {busy ? 'Loading…' : 'Show older'}
        </button>
      )}
      {events && <p className="hint">Events are kept for {days} days.</p>}
    </>
  )
}
