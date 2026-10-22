import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type TrackedDomain } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { DomainBadge, domainLeft } from '../components/Domains'
import { Field } from '../components/Field'

const day = (iso: string) => new Date(iso).toLocaleDateString([], { dateStyle: 'medium' })

function Expiry({ d }: { d: TrackedDomain }) {
  if (!d.tracked) {
    return <span className="hint">Over your plan’s limit, so it is not looked up. <Link to="/billing">See plans</Link></span>
  }
  if (d.status === 'unavailable') {
    return <span className="hint">{d.unavailable_reason ?? 'The registry does not publish an expiry date for this domain.'}</span>
  }
  if (d.status === 'pending' || !d.expires_at) return <span className="hint">HawkEye is looking this up. Check back soon.</span>
  return (
    <>
      {day(d.expires_at)} <span className="hint">({domainLeft(d.expires_at)})</span>
      {d.lookup_error && <div className="hint">The latest lookup failed, so this is the last date HawkEye saw.</div>}
    </>
  )
}

export function DomainsPage() {
  const { organizations } = useAuth()
  const isOwner = organizations[0]?.role === 'owner'

  const [items, setItems] = useState<TrackedDomain[] | null>(null)
  const [limit, setLimit] = useState(0)
  const [error, setError] = useState<ApiError | string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [name, setName] = useState('')

  const load = useCallback(async () => {
    try {
      const r = await api<{ domains: TrackedDomain[]; limit: number }>('GET', '/domains')
      setItems(r.domains)
      setLimit(r.limit)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load domains.')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function add(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setFields({})
    try {
      await api('POST', '/domains', { name })
      setName('')
      await load()
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.fields).length > 0) setFields(err.fields)
      else setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function remove(d: TrackedDomain) {
    const extra = d.monitor_count > 0 ? ' Your monitors still use it, so it stays in the list, but it is no longer pinned.' : ''
    if (!window.confirm(`Stop tracking ${d.name}?${extra}`)) return
    try {
      await api('DELETE', `/domains/${d.id}`)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    }
  }

  const upgrade = error instanceof ApiError && error.code === 'PLAN_LIMIT_REACHED'
  const tracked = items?.filter((d) => d.tracked).length ?? 0
  const over = (items?.length ?? 0) - tracked

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.websites} />
        <h1>Domains</h1>
      </div>
      <p>
        A domain that lapses takes its website and email down with it. HawkEye looks up when each domain’s registration expires and emails you 30, 14, 7 and 1 day before,
        and again if it expires. Domains come from your monitors automatically; you can also add others, such as a domain you own but do not monitor.
      </p>
      <p className="hint">
        Not every registry shares expiry dates, and some hide them for privacy. Those domains show “Expiry unavailable”, which does not mean the domain is safe: check the
        date with your registrar.
      </p>

      {error && (
        <Alert>
          {typeof error === 'string' ? error : error.message} {upgrade && <Link to="/billing">See plans</Link>}
        </Alert>
      )}
      {items === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {items && items.length === 0 && <p className="hint">No domains yet. Add a website to monitor, or add a domain below.</p>}
      {items && items.length > 0 && (
        <>
          <p className="hint" role="status">
            Tracking {tracked} of {limit} domain{limit === 1 ? '' : 's'} included in your plan.
            {over > 0 && <> {over} more {over === 1 ? 'is' : 'are'} listed but not looked up. <Link to="/billing">See plans</Link></>}
          </p>
          <div className="table-wrap">
            <table>
              <caption className="sr-only">Tracked domains</caption>
              <thead>
                <tr><th>Domain</th><th>Registration expires</th><th>Status</th><th>Used by</th>{isOwner && <th>Actions</th>}</tr>
              </thead>
              <tbody>
                {items.map((d) => (
                  <tr key={d.id}>
                    <td>
                      {d.name}
                      {d.manual && <div className="hint">Added by you</div>}
                    </td>
                    <td><Expiry d={d} /></td>
                    <td><DomainBadge domain={d} /></td>
                    <td>{d.monitor_count > 0 ? <Link to="/monitors">{d.monitor_count} monitor{d.monitor_count === 1 ? '' : 's'}</Link> : <span className="hint">No monitors</span>}</td>
                    {isOwner && (
                      <td className="actions">
                        {d.manual && <button className="danger" onClick={() => void remove(d)} aria-label={`Stop tracking ${d.name}`}>Remove</button>}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {isOwner ? (
        <section className="card narrow-left" aria-labelledby="add-h">
          <h2 id="add-h">Add a domain</h2>
          <form onSubmit={add}>
            <Field label="Domain" required maxLength={253} value={name} onChange={(e) => setName(e.target.value)} error={fields.name} placeholder="example.com"
              hint="You can paste any website address; HawkEye tracks the registered domain (https://www.example.com/shop becomes example.com)." />
            <button type="submit" disabled={busy}>Add domain</button>
          </form>
        </section>
      ) : (
        <p className="hint">Only the organization owner can add or remove domains.</p>
      )}
    </>
  )
}
