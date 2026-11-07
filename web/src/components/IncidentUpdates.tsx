import { useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type Incident, type IncidentUpdate, type IncidentUpdateStatus, type StatusPageRef } from '../api'
import { Alert } from './Alert'
import { Appear } from './Appear'

const MAX_UPDATE_LENGTH = 1000

// Full class names so the CSS generator can see them. Shared with the public status page.
const UPDATE_STATUS: Record<IncidentUpdateStatus, { label: string; cls: string }> = {
  investigating: { label: 'Investigating', cls: 'd-badge d-badge-soft d-badge-warning' },
  identified: { label: 'Identified', cls: 'd-badge d-badge-soft d-badge-error' },
  monitoring: { label: 'Monitoring', cls: 'd-badge d-badge-soft d-badge-info' },
  resolved: { label: 'Resolved', cls: 'd-badge d-badge-soft d-badge-success' },
}

/** The status of an update, as a badge. An unknown status (from a newer server) shows as its own name. */
export function UpdateStatusBadge({ status }: { status: string }) {
  const s = UPDATE_STATUS[status as IncidentUpdateStatus]
  return <span className={s?.cls ?? 'd-badge d-badge-soft'}>{s?.label ?? status}</span>
}

const when = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

/**
 * An incident's updates, and (for owners) the form to post one. Updates are public on every published status page that lists the monitor,
 * so the panel says where they will appear, or that nobody outside the team will see them.
 */
export function IncidentUpdates({ incident, isOwner, onCountChange }: { incident: Incident; isOwner: boolean; onCountChange?: (n: number) => void }) {
  const [updates, setUpdates] = useState<IncidentUpdate[] | null>(null)
  const [pages, setPages] = useState<StatusPageRef[]>([])
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<IncidentUpdateStatus>(incident.status === 'open' ? 'investigating' : 'resolved')
  const [message, setMessage] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [justPosted, setJustPosted] = useState<string | null>(null)
  const base = `/incidents/${incident.id}/updates`

  useEffect(() => {
    let live = true
    api<{ updates: IncidentUpdate[]; status_pages: StatusPageRef[] }>('GET', base).then(
      (r) => {
        if (!live) return
        setUpdates(r.updates)
        setPages(r.status_pages)
      },
      (e: unknown) => live && setError(e instanceof ApiError ? e.message : 'Could not load the updates.'),
    )
    return () => {
      live = false
    }
  }, [base])

  function changed(next: IncidentUpdate[]) {
    setUpdates(next)
    onCountChange?.(next.length)
  }

  async function post(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setFieldError(null)
    try {
      const r = await api<{ update: IncidentUpdate }>('POST', base, { status, message })
      changed([r.update, ...(updates ?? [])])
      setJustPosted(r.update.id)
      setMessage('')
      if (status !== 'resolved') setStatus(status === 'investigating' ? 'identified' : status === 'identified' ? 'monitoring' : 'resolved')
    } catch (err) {
      const fields = err instanceof ApiError ? (err.details?.fields as Record<string, string> | undefined) : undefined
      if (fields?.message) setFieldError(`This update ${fields.message}.`)
      else setError(err instanceof ApiError ? err.message : 'Could not post the update. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function remove(u: IncidentUpdate) {
    if (!window.confirm(pages.length ? 'Delete this update? It also disappears from the public status page.' : 'Delete this update?')) return
    try {
      await api('DELETE', `${base}/${u.id}`)
      changed((updates ?? []).filter((x) => x.id !== u.id))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not delete the update.')
    }
  }

  const left = MAX_UPDATE_LENGTH - message.length
  const fieldID = `update-${incident.id}`
  return (
    <div className="incident-updates">
      {error && <Alert>{error}</Alert>}
      {updates !== null && (pages.length > 0 ? (
        <p className="hint">
          Shown publicly on {pages.map((p, n) => (
            <span key={p.slug}>{n > 0 && ', '}<a href={`/status/${p.slug}`} target="_blank" rel="noopener noreferrer">{p.title}</a></span>
          ))}.
        </p>
      ) : (
        <Alert tone="info">This website is not on a published status page, so only your team sees these updates.</Alert>
      ))}

      {isOwner ? (
        <form className="update-form" onSubmit={(e) => void post(e)}>
          <div className="field">
            <label htmlFor={`${fieldID}-status`}>Status</label>
            <select id={`${fieldID}-status`} value={status} onChange={(e) => setStatus(e.target.value as IncidentUpdateStatus)}>
              {(Object.keys(UPDATE_STATUS) as IncidentUpdateStatus[]).map((s) => <option key={s} value={s}>{UPDATE_STATUS[s].label}</option>)}
            </select>
          </div>
          <div className="field">
            <label htmlFor={`${fieldID}-message`}>Update</label>
            <textarea id={`${fieldID}-message`} rows={3} maxLength={MAX_UPDATE_LENGTH} value={message} required
              placeholder="What is happening, and what you are doing about it"
              aria-describedby={`${fieldID}-count`} aria-invalid={fieldError ? true : undefined}
              onChange={(e) => setMessage(e.target.value)} />
            <small id={`${fieldID}-count`} className="hint">{left} characters left. Plain text: links and formatting are shown as typed.</small>
            {fieldError && <small className="error">{fieldError}</small>}
          </div>
          <button type="submit" disabled={busy || !message.trim()}>{busy ? 'Posting…' : 'Post update'}</button>
        </form>
      ) : (
        <p className="hint">Only the organization’s owner can post updates.</p>
      )}

      {updates === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading updates…</p>}
      {updates?.length === 0 && <p className="hint">No updates yet.</p>}
      {updates && updates.length > 0 && (
        <ol className="update-list" aria-label="Updates, newest first">
          {updates.map((u) => {
            const item = (
              <>
                <div className="update-head">
                  <UpdateStatusBadge status={u.status} />
                  <time dateTime={u.created_at}>{when(u.created_at)}</time>
                  {u.author_email && <span className="hint">by {u.author_email}</span>}
                  {isOwner && <button type="button" className="linklike" onClick={() => void remove(u)} aria-label={`Delete the update from ${when(u.created_at)}`}>Delete</button>}
                </div>
                <p className="update-message">{u.message}</p>
              </>
            )
            return <li key={u.id}>{u.id === justPosted ? <Appear>{item}</Appear> : item}</li>
          })}
        </ol>
      )}
    </div>
  )
}
