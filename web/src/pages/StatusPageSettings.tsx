import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type Monitor, type StatusPageConfig } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

/** The owner's editor for the public status page. */
export function StatusPageSettings() {
  const { organizations } = useAuth()
  const isOwner = organizations[0]?.role === 'owner'

  const [loaded, setLoaded] = useState(false)
  const [exists, setExists] = useState(false)
  const [monitors, setMonitors] = useState<Monitor[]>([])
  const [slug, setSlug] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [published, setPublished] = useState(false)
  const [shown, setShown] = useState<Record<string, string | undefined>>({}) // monitor id -> display name ('' = own name); absent = not shown
  const [error, setError] = useState<string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const [p, m] = await Promise.all([
        api<{ status_page: StatusPageConfig | null }>('GET', '/status-page'),
        api<{ monitors: Monitor[] }>('GET', '/monitors?limit=100'),
      ])
      setMonitors(m.monitors)
      if (p.status_page) {
        setExists(true)
        setSlug(p.status_page.slug)
        setTitle(p.status_page.title)
        setDescription(p.status_page.description)
        setPublished(p.status_page.published)
        setShown(Object.fromEntries(p.status_page.monitors.map((x) => [x.monitor_id, x.display_name])))
      }
      setLoaded(true)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load the status page settings.')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setNotice(null)
    setFields({})
    try {
      const ordered = monitors.filter((m) => m.id in shown)
      await api('PUT', '/status-page', {
        slug, title, description, published, monitors: ordered.map((m) => ({ monitor_id: m.id, display_name: shown[m.id] ?? '' })),
      })
      setExists(true)
      setNotice(published ? 'Saved. Your status page is public.' : 'Saved. Your status page is a draft and is not visible to anyone yet.')
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.fields).length > 0) setFields(err.fields)
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!window.confirm('Delete your status page? Its address will stop working.')) return
    try {
      await api('DELETE', '/status-page')
      setExists(false)
      setPublished(false)
      setNotice('Status page deleted.')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    }
  }

  const link = `${window.location.origin}/status/${slug}`

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.analytics} />
        <h1>Status page</h1>
      </div>
      <p>
        Give your customers a public page showing whether your services are up, their daily uptime and recent incidents. Only the monitors you choose appear, under
        the names you choose — web addresses and error details are never shown.
      </p>
      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="success" role="status">{notice}</Alert>}
      {exists && published && (
        <p>
          Public at <a href={`/status/${slug}`} target="_blank" rel="noreferrer">{link}</a>
        </p>
      )}
      {!loaded && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {loaded && (
        <form onSubmit={save} className="card narrow-left">
          <fieldset disabled={!isOwner || busy} className="plain">
            <Field label="Address" required value={slug} onChange={(e) => setSlug(e.target.value.toLowerCase())} error={fields.slug} maxLength={40}
              hint={`${window.location.origin}/status/… — 3 to 40 lowercase letters, numbers and hyphens.`} />
            <Field label="Title" required maxLength={100} value={title} onChange={(e) => setTitle(e.target.value)} error={fields.title} placeholder="Acme status" />
            <Field label="Description (optional)" maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} error={fields.description} />
            <fieldset className="field">
              <legend>Monitors to show</legend>
              {monitors.length === 0 && <p className="hint">Add a monitor first.</p>}
              {monitors.map((m) => (
                <div key={m.id} className="kv-row">
                  <label>
                    <input type="checkbox" checked={m.id in shown}
                      onChange={(e) => {
                        const next = { ...shown }
                        if (e.target.checked) next[m.id] = ''
                        else delete next[m.id]
                        setShown(next)
                      }} /> {m.name}
                  </label>
                  {m.id in shown && (
                    <input aria-label={`Public name for ${m.name}`} placeholder={m.name} maxLength={100} value={shown[m.id] ?? ''}
                      onChange={(e) => setShown({ ...shown, [m.id]: e.target.value })} />
                  )}
                </div>
              ))}
              {fields.monitors && <small className="error">{fields.monitors}</small>}
            </fieldset>
            <label>
              <input type="checkbox" className="d-toggle d-toggle-sm" checked={published} onChange={(e) => setPublished(e.target.checked)} /> Publish — anyone with the link can see this page
            </label>
          </fieldset>
          {isOwner ? (
            <p>
              <button type="submit" disabled={busy}>Save status page</button>{' '}
              {exists && <button type="button" className="danger" onClick={() => void remove()}>Delete</button>}
            </p>
          ) : (
            <p className="hint">Only the organization owner can change the status page.</p>
          )}
        </form>
      )}
    </>
  )
}
