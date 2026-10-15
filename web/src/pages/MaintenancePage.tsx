import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type MaintenanceWindow, type Monitor } from '../api'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

const REPEAT_LABEL: Record<MaintenanceWindow['repeat'], string> = { none: 'One time', daily: 'Every day', weekly: 'Every week' }
const DURATIONS = [
  { minutes: 15, label: '15 minutes' },
  { minutes: 30, label: '30 minutes' },
  { minutes: 60, label: '1 hour' },
  { minutes: 120, label: '2 hours' },
  { minutes: 240, label: '4 hours' },
  { minutes: 480, label: '8 hours' },
  { minutes: 1440, label: '1 day' },
]

const when = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

/** The value a datetime-local input expects for "now + offset", in the browser's own time zone. */
function localInputValue(offsetMinutes: number): string {
  const d = new Date(Date.now() + offsetMinutes * 60_000)
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset())
  return d.toISOString().slice(0, 16)
}

function StatusLabel({ w }: { w: MaintenanceWindow }) {
  if (w.status === 'active') return <span className="badge maintenance"><span aria-hidden="true">🛠</span> In progress</span>
  if (w.status === 'ended') return <span className="badge paused"><span aria-hidden="true">✔</span> Finished</span>
  return <span className="badge unknown"><span aria-hidden="true">⏳</span> Scheduled</span>
}

export function MaintenancePage() {
  const { organizations } = useAuth()
  const isOwner = organizations[0]?.role === 'owner'

  const [items, setItems] = useState<MaintenanceWindow[] | null>(null)
  const [monitors, setMonitors] = useState<Monitor[]>([])
  const [error, setError] = useState<ApiError | string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)

  const [name, setName] = useState('')
  const [start, setStart] = useState(() => localInputValue(60))
  const [minutes, setMinutes] = useState(60)
  const [repeat, setRepeat] = useState<MaintenanceWindow['repeat']>('none')
  const [all, setAll] = useState(true)
  const [picked, setPicked] = useState<string[]>([])

  const load = useCallback(async () => {
    try {
      const [w, m] = await Promise.all([
        api<{ maintenance_windows: MaintenanceWindow[] }>('GET', '/maintenance-windows'),
        api<{ monitors: Monitor[] }>('GET', '/monitors?limit=100'),
      ])
      setItems(w.maintenance_windows)
      setMonitors(m.monitors)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load maintenance windows.')
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
      await api('POST', '/maintenance-windows', {
        name, starts_at: new Date(start).toISOString(), duration_minutes: minutes, repeat, all_monitors: all, monitor_ids: all ? [] : picked,
      })
      setName('')
      await load()
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.fields).length > 0) setFields(err.fields)
      else setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function remove(w: MaintenanceWindow) {
    if (!window.confirm(`Delete the maintenance window “${w.name}”?`)) return
    try {
      await api('DELETE', `/maintenance-windows/${w.id}`)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    }
  }

  const covers = (w: MaintenanceWindow) =>
    w.all_monitors ? 'All monitors' : w.monitor_ids.map((id) => monitors.find((m) => m.id === id)?.name ?? 'Deleted monitor').join(', ')
  const upgrade = error instanceof ApiError && error.code === 'PLAN_LIMIT_REACHED'

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.settings} />
        <h1>Maintenance windows</h1>
      </div>
      <p>
        Schedule planned work so it doesn’t page you. During a window HawkEye keeps checking, but a monitor’s status does not change, no incident is opened, no alert is
        sent, and those checks don’t count against uptime.
      </p>

      {error && (
        <p role="alert" className="error">
          {typeof error === 'string' ? error : error.message} {upgrade && <Link to="/billing">See plans</Link>}
        </p>
      )}
      {items === null && !error && <p role="status">Loading…</p>}

      {items && items.length === 0 && <p className="hint">No maintenance windows yet.</p>}
      {items && items.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Maintenance windows</caption>
            <thead>
              <tr><th>Name</th><th>When</th><th>Applies to</th><th>Status</th>{isOwner && <th>Actions</th>}</tr>
            </thead>
            <tbody>
              {items.map((w) => (
                <tr key={w.id}>
                  <td>{w.name}</td>
                  <td>
                    {when(w.status === 'upcoming' && w.next_start ? w.next_start : w.starts_at)} · {DURATIONS.find((d) => d.minutes === w.duration_minutes)?.label ?? `${w.duration_minutes} minutes`}
                    <span className="hint"> ({REPEAT_LABEL[w.repeat]})</span>
                    {w.status === 'active' && w.repeat !== 'none' && w.next_start && <span className="hint"> · next {when(w.next_start)}</span>}
                  </td>
                  <td>{covers(w)}</td>
                  <td><StatusLabel w={w} /></td>
                  {isOwner && (
                    <td className="actions">
                      <button className="danger" onClick={() => void remove(w)} aria-label={`Delete ${w.name}`}>Delete</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {isOwner ? (
        <section className="card narrow-left" aria-labelledby="add-h">
          <h2 id="add-h">Schedule a window</h2>
          <form onSubmit={add}>
            <Field label="Name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={fields.name} placeholder="Database upgrade" />
            <Field label="Starts" type="datetime-local" required value={start} onChange={(e) => setStart(e.target.value)} error={fields.starts_at}
              hint="In your local time. A start in the past begins the window right away." />
            <div className="field">
              <label htmlFor="duration">Lasts</label>
              <select id="duration" value={minutes} onChange={(e) => setMinutes(Number(e.target.value))}>
                {DURATIONS.map((d) => (
                  <option key={d.minutes} value={d.minutes}>{d.label}</option>
                ))}
              </select>
              {fields.duration_minutes && <small className="error">{fields.duration_minutes}</small>}
            </div>
            <div className="field">
              <label htmlFor="repeat">Repeats</label>
              <select id="repeat" value={repeat} onChange={(e) => setRepeat(e.target.value as MaintenanceWindow['repeat'])}>
                <option value="none">Never (one time)</option>
                <option value="daily">Every day</option>
                <option value="weekly">Every week</option>
              </select>
              {repeat !== 'none' && <small className="hint">Repeats every {repeat === 'daily' ? '24 hours' : '7 days'} from the start time, so it shifts by an hour when your clocks change.</small>}
            </div>
            <fieldset className="field">
              <legend>Applies to</legend>
              <label><input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} /> All monitors, including ones added later</label>
              {!all &&
                monitors.map((m) => (
                  <label key={m.id}>
                    <input type="checkbox" checked={picked.includes(m.id)}
                      onChange={(e) => setPicked(e.target.checked ? [...picked, m.id] : picked.filter((id) => id !== m.id))} /> {m.name}
                  </label>
                ))}
              {fields.monitor_ids && <small className="error">{fields.monitor_ids}</small>}
            </fieldset>
            <button type="submit" disabled={busy}>Schedule window</button>
          </form>
        </section>
      ) : (
        <p className="hint">Only the organization owner can schedule maintenance windows.</p>
      )}
    </>
  )
}
