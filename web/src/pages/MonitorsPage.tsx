import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type Monitor, type Usage } from '../api'
import { formatMoney } from '../format'
import { Alert } from '../components/Alert'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { CertBadge } from '../components/Certificate'
import { StatusBadge } from '../components/StatusBadge'

const PAGE_SIZE = 25

function money(cents: number) {
  return cents === 0 ? 'Free' : `${formatMoney(cents)}/month`
}

export function UsagePanel({ usage }: { usage: Usage }) {
  const { monitors_used: used, monitor_limit: limit, plan, next_plan: next, over_limit } = usage
  const atLimit = used >= limit
  return (
    <section className="card" aria-labelledby="usage-h">
      <h2 id="usage-h">{plan.name} plan</h2>
      <p>
        <strong>{used}</strong> of <strong>{limit}</strong> monitors used
      </p>
      <progress max={limit} value={Math.min(used, limit)} aria-label={`${used} of ${limit} monitors used`} />
      {over_limit && (
        <Alert>
          You have more monitors than your plan includes. Nothing was deleted, but you can't add more until you remove{' '}
          {used - limit} or upgrade.
        </Alert>
      )}
      {atLimit && !over_limit && <p role="status">You've used all of your monitors.</p>}
      {atLimit && next && (
        <p>
          {next.name} ({money(next.monthly_price_cents)}) includes {next.max_monitors} monitors and checks every{' '}
          {Math.round(next.minimum_check_interval_seconds / 60)} minute(s).
        </p>
      )}
    </section>
  )
}

export function MonitorsPage() {
  const [monitors, setMonitors] = useState<Monitor[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [usage, setUsage] = useState<Usage | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [list, u] = await Promise.all([
        api<{ monitors: Monitor[]; total: number }>('GET', `/monitors?limit=${PAGE_SIZE}&offset=${offset}`),
        api<{ usage: Usage }>('GET', '/usage'),
      ])
      setMonitors(list.monitors)
      setTotal(list.total)
      setUsage(u.usage)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load monitors.')
    }
  }, [offset])

  useEffect(() => {
    void load()
  }, [load])

  async function act(fn: () => Promise<unknown>) {
    try {
      await fn()
      await load()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
    }
  }

  function remove(m: Monitor) {
    if (window.confirm(`Delete "${m.name}"? Its monitoring stops and this cannot be undone.`)) {
      void act(() => api('DELETE', `/monitors/${m.id}`))
    }
  }

  const canAdd = usage ? usage.monitors_used < usage.monitor_limit : true

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.monitors} />
        <h1>Monitors</h1>
        <span className="spacer" />
        {canAdd ? (
          <Link className="button" to="/monitors/new">Add monitor</Link>
        ) : (
          <button disabled aria-describedby="limit-reason">Add monitor</button>
        )}
      </div>
      {!canAdd && <p id="limit-reason" className="hint">Plan limit reached — see the usage box below.</p>}
      {error && <Alert>{error}</Alert>}
      {usage && <UsagePanel usage={usage} />}

      {monitors === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading monitors…</p>}
      {monitors?.length === 0 && (
        <section className="card empty">
          <h2>Add your first website</h2>
          <p>Your watch starts here. HawkEye will check it regularly and alert you when an outage is confirmed.</p>
          <Link className="button" to="/monitors/new">Add your first website</Link>
        </section>
      )}
      {monitors && monitors.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Your monitors</caption>
            <thead>
              <tr><th>Name</th><th>Status</th><th>SSL</th><th>Interval</th><th>Last check</th><th>Actions</th></tr>
            </thead>
            <tbody>
              {monitors.map((m) => (
                <tr key={m.id}>
                  <td>
                    <Link to={`/monitors/${m.id}`}>{m.name}</Link>
                    <div className="hint mono">{m.url}</div>
                  </td>
                  <td><StatusBadge monitor={m} /></td>
                  <td><CertBadge monitor={m} /></td>
                  <td>every {Math.round(m.effective_check_interval_seconds / 60)} min</td>
                  <td>{m.last_checked_at ? new Date(m.last_checked_at).toLocaleString() : 'Not yet'}</td>
                  <td className="actions">
                    <button onClick={() => void act(() => api('POST', `/monitors/${m.id}/${m.enabled ? 'pause' : 'resume'}`))}>
                      {m.enabled ? 'Pause' : 'Resume'}
                    </button>
                    <Link className="button" to={`/monitors/${m.id}/edit`}>Edit</Link>
                    <button className="danger" onClick={() => remove(m)} aria-label={`Delete ${m.name}`}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {total > PAGE_SIZE && (
        <nav className="row" aria-label="Pagination">
          <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>Previous</button>
          <span>{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
          <button disabled={offset + PAGE_SIZE >= total} onClick={() => setOffset(offset + PAGE_SIZE)}>Next</button>
        </nav>
      )}
    </>
  )
}
