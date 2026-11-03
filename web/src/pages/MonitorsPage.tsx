import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, MAX_TAG_NAME, type BulkAction, type Monitor, type MonitorStatus, type TagSummary, type Usage } from '../api'
import { formatMoney } from '../format'
import { Alert } from '../components/Alert'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { CertBadge } from '../components/Certificate'
import { StatusBadge } from '../components/StatusBadge'
import { TagList } from '../components/Tags'
import { BulkAlertTiming, type AlertTimingChange } from '../components/AlertTiming'

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

const plural = (n: number) => `${n} monitor${n === 1 ? '' : 's'}`

/** What a finished bulk action did, for the status line. */
const DONE: Record<BulkAction, string> = {
  pause: 'Paused', resume: 'Resumed', delete: 'Deleted', set_interval: 'Changed the interval of',
  add_tags: 'Tagged', remove_tags: 'Removed the tag from', set_alert_timing: 'Changed the alert timing of',
}

type BulkExtra = { check_interval_seconds?: number; tag_ids?: string[] } & AlertTimingChange

/** Acts on the selected monitors. Every action is all or nothing on the server. */
export function BulkBar({ count, tags, minMinutes, onRun, onClear }: {
  count: number
  tags: TagSummary[]
  minMinutes: number
  onRun: (action: BulkAction, extra?: BulkExtra) => void
  onClear: () => void
}) {
  const [minutes, setMinutes] = useState('')
  const [timing, setTiming] = useState(false)
  const [addTag, setAddTag] = useState('')
  const [removeTag, setRemoveTag] = useState('')
  return (
    <div className="bulk-bar" role="region" aria-label="Bulk actions">
      <strong>{plural(count)} selected</strong>
      <button type="button" onClick={() => onRun('pause')}>Pause</button>
      <button type="button" onClick={() => onRun('resume')}>Resume</button>
      {tags.length > 0 && (
        <>
          <select aria-label="Tag to add" value={addTag} onChange={(e) => setAddTag(e.target.value)}>
            <option value="">Add tag…</option>
            {tags.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <button type="button" disabled={!addTag} onClick={() => onRun('add_tags', { tag_ids: [addTag] })}>Add</button>
          <select aria-label="Tag to remove" value={removeTag} onChange={(e) => setRemoveTag(e.target.value)}>
            <option value="">Remove tag…</option>
            {tags.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <button type="button" disabled={!removeTag} onClick={() => onRun('remove_tags', { tag_ids: [removeTag] })}>Remove</button>
        </>
      )}
      <input type="number" aria-label="Check every (minutes)" placeholder="min" min={minMinutes} max={1440} value={minutes}
        onChange={(e) => setMinutes(e.target.value)} />
      <button type="button" disabled={minutes === ''} onClick={() => onRun('set_interval', { check_interval_seconds: Number(minutes) * 60 })}>
        Set interval
      </button>
      <button type="button" aria-expanded={timing} onClick={() => setTiming(!timing)}>Alert timing…</button>
      <button type="button" className="danger" onClick={() => onRun('delete')}>Delete</button>
      <span className="spacer" />
      <button type="button" className="linklike" onClick={onClear}>Clear selection</button>
      {timing && (
        <BulkAlertTiming onCancel={() => setTiming(false)} onApply={(change) => {
          setTiming(false)
          onRun('set_alert_timing', change)
        }} />
      )}
    </div>
  )
}

/** Rename, recolour and delete the organization's tags. */
function TagManager({ tags, onChanged }: { tags: TagSummary[]; onChanged: () => void }) {
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState('')

  async function run(fn: () => Promise<unknown>) {
    try {
      await fn()
      setError(null)
      onChanged()
    } catch (e) {
      setError(e instanceof ApiError ? (e.fields.name ?? e.fields.color ?? e.message) : 'That did not work. Try again.')
    }
  }

  return (
    <details className="card tag-manager">
      <summary>Manage tags ({tags.length})</summary>
      {error && <Alert>{error}</Alert>}
      {tags.length === 0 && <p className="hint">No tags yet. Tag monitors by client or project to filter the list and change several at once.</p>}
      <ul>
        {tags.map((t) => (
          <li key={t.id}>
            <input type="color" aria-label={`Colour of ${t.name}`} defaultValue={t.color ?? '#888888'}
              onBlur={(e) => {
                const v = e.target.value
                if (v !== (t.color ?? '#888888')) void run(() => api('PATCH', `/tags/${t.id}`, { color: v }))
              }} />
            <input aria-label={`Name of ${t.name}`} defaultValue={t.name} maxLength={MAX_TAG_NAME}
              onBlur={(e) => {
                const v = e.target.value.trim()
                if (v && v !== t.name) void run(() => api('PATCH', `/tags/${t.id}`, { name: v }))
              }} />
            <span className="hint">{plural(t.monitor_count)}</span>
            {t.color && (
              <button type="button" className="linklike" onClick={() => void run(() => api('PATCH', `/tags/${t.id}`, { color: null }))}>
                Reset colour
              </button>
            )}
            <span className="spacer" />
            <button type="button" className="danger" aria-label={`Delete tag ${t.name}`}
              onClick={() => {
                if (window.confirm(`Delete the tag "${t.name}"? Its monitors are kept.`)) void run(() => api('DELETE', `/tags/${t.id}`))
              }}>
              Delete
            </button>
          </li>
        ))}
      </ul>
      <form className="kv-row" onSubmit={(e) => {
        e.preventDefault()
        if (name.trim()) void run(async () => {
          await api('POST', '/tags', { name: name.trim() })
          setName('')
        })
      }}>
        <input aria-label="New tag name" placeholder="New tag" maxLength={MAX_TAG_NAME} value={name} onChange={(e) => setName(e.target.value)} />
        <button type="submit" disabled={name.trim() === ''}>Create tag</button>
      </form>
    </details>
  )
}

export function MonitorsPage() {
  const [monitors, setMonitors] = useState<Monitor[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [usage, setUsage] = useState<Usage | null>(null)
  const [tags, setTags] = useState<TagSummary[]>([])
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('') // query, applied after typing pauses
  const [status, setStatus] = useState<MonitorStatus | ''>('')
  const [tag, setTag] = useState('')
  const [selected, setSelected] = useState<string[]>([])

  // A new filter starts from the first page with nothing selected; these updates are batched, so the list loads once.
  const restart = () => {
    setOffset(0)
    setSelected([])
  }

  useEffect(() => {
    const t = setTimeout(() => {
      const v = query.trim()
      if (v !== search) {
        setSearch(v)
        restart()
      }
    }, 300)
    return () => clearTimeout(t)
  }, [query, search])

  const load = useCallback(async () => {
    const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(offset) })
    if (search) params.set('q', search)
    if (status) params.set('status', status)
    if (tag) params.set('tag', tag)
    try {
      const [list, u, t] = await Promise.all([
        api<{ monitors: Monitor[]; total: number }>('GET', `/monitors?${params}`),
        api<{ usage: Usage }>('GET', '/usage'),
        // Tags only add filters and bulk actions; the list still works without them.
        api<{ tags?: TagSummary[] }>('GET', '/tags').catch(() => ({ tags: [] })),
      ])
      setMonitors(list.monitors)
      setTotal(list.total)
      setUsage(u.usage)
      setTags(t.tags ?? [])
      // Keep only selections still on the page.
      setSelected((s) => s.filter((id) => list.monitors.some((m) => m.id === id)))
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load monitors.')
    }
  }, [offset, search, status, tag])

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

  async function bulk(action: BulkAction, extra: BulkExtra = {}) {
    const n = selected.length
    if (action === 'delete' && !window.confirm(`Delete ${plural(n)}? Their monitoring stops and this cannot be undone.`)) return
    setNotice(null)
    try {
      await api('POST', '/monitors/bulk', { monitor_ids: selected, action, ...extra })
      setNotice(`${DONE[action]} ${plural(n)}.`)
      if (action === 'delete') setSelected([])
      await load()
    } catch (e) {
      if (e instanceof ApiError) {
        const field = Object.values(e.fields)[0]
        setError(field ? `${e.message} ${field[0].toUpperCase()}${field.slice(1)}.` : e.message)
      } else {
        setError('That did not work. Try again.')
      }
    }
  }

  const canAdd = usage ? usage.monitors_used < usage.monitor_limit : true
  const filtered = search !== '' || status !== '' || tag !== ''
  const allOnPage = monitors !== null && monitors.length > 0 && monitors.every((m) => selected.includes(m.id))
  const minMinutes = usage ? Math.ceil(usage.plan.minimum_check_interval_seconds / 60) : 1
  const toggle = (id: string, on: boolean) => setSelected((s) => (on ? [...s, id] : s.filter((x) => x !== id)))

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
      {notice && <p role="status">{notice}</p>}
      {usage && <UsagePanel usage={usage} />}

      {(filtered || (monitors?.length ?? 0) > 0) && (
        <div className="monitor-filters" role="search">
          <input type="search" aria-label="Search monitors" placeholder="Search by name or URL" value={query}
            onChange={(e) => setQuery(e.target.value)} />
          <select aria-label="Filter by status" value={status} onChange={(e) => { setStatus(e.target.value as MonitorStatus | ''); restart() }}>
            <option value="">Any status</option>
            <option value="up">Online</option>
            <option value="down">Offline</option>
            <option value="unknown">Pending first check</option>
          </select>
          {tags.length > 0 && (
            <select aria-label="Filter by tag" value={tag} onChange={(e) => { setTag(e.target.value); restart() }}>
              <option value="">All tags</option>
              {tags.map((t) => <option key={t.id} value={t.id}>{t.name} ({t.monitor_count})</option>)}
            </select>
          )}
          {filtered && (
            <button type="button" className="linklike" onClick={() => { setQuery(''); setSearch(''); setStatus(''); setTag(''); restart() }}>
              Clear filters
            </button>
          )}
        </div>
      )}

      {monitors === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading monitors…</p>}
      {monitors?.length === 0 && !filtered && (
        <section className="card empty">
          <h2>Add your first website</h2>
          <p>Your watch starts here. HawkEye will check it regularly and alert you when an outage is confirmed.</p>
          <Link className="button" to="/monitors/new">Add your first website</Link>
        </section>
      )}
      {monitors?.length === 0 && filtered && <p>No monitors match these filters.</p>}

      {selected.length > 0 && (
        <BulkBar count={selected.length} tags={tags} minMinutes={minMinutes} onRun={(a, x) => void bulk(a, x)} onClear={() => setSelected([])} />
      )}
      {monitors && monitors.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Your monitors</caption>
            <thead>
              <tr>
                <th className="select-col">
                  <input type="checkbox" aria-label="Select all monitors on this page" checked={allOnPage}
                    onChange={(e) => setSelected(e.target.checked ? monitors.map((m) => m.id) : [])} />
                </th>
                <th>Name</th><th>Status</th><th>SSL</th><th>Interval</th><th>Last check</th><th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {monitors.map((m) => (
                <tr key={m.id}>
                  <td className="select-col">
                    <input type="checkbox" aria-label={`Select ${m.name}`} checked={selected.includes(m.id)}
                      onChange={(e) => toggle(m.id, e.target.checked)} />
                  </td>
                  <td>
                    <Link to={`/monitors/${m.id}`}>{m.name}</Link>
                    <div className="hint mono">{m.url}</div>
                    <TagList tags={m.tags ?? []} />
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
          <button disabled={offset === 0} onClick={() => { setSelected([]); setOffset(Math.max(0, offset - PAGE_SIZE)) }}>Previous</button>
          <span>{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
          <button disabled={offset + PAGE_SIZE >= total} onClick={() => { setSelected([]); setOffset(offset + PAGE_SIZE) }}>Next</button>
        </nav>
      )}
      {monitors !== null && (monitors.length > 0 || tags.length > 0) && <TagManager tags={tags} onChanged={() => void load()} />}
    </>
  )
}
