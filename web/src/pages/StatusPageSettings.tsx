import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router'
import { api, ApiError, apiUpload, type Monitor, type StatusPageConfig, type Usage } from '../api'
import { readableTextOn } from '../color'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

/** A notice passed to the list after saving or deleting a page. */
type ListState = { notice?: string } | null

/** The organization's status pages: one per client or product, up to the plan's limit. */
export function StatusPagesPage() {
  const { organization } = useAuth()
  const isOwner = organization?.role === 'owner'
  const location = useLocation()
  const notice = (location.state as ListState)?.notice

  const [pages, setPages] = useState<StatusPageConfig[] | null>(null)
  const [max, setMax] = useState(1)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api<{ status_pages: StatusPageConfig[]; max: number }>('GET', '/status-pages')
      .then((r) => {
        setPages(r.status_pages)
        setMax(r.max)
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load your status pages.'))
  }, [])

  const full = pages !== null && pages.length >= max
  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.statusPage} />
        <h1>Status pages</h1>
        <span className="spacer" />
        {isOwner && pages !== null && !full && <Link className="button primary" to="/status-pages/new">New status page</Link>}
      </div>
      <p>
        Give your customers a public page showing whether your services are online, their daily uptime and recent incidents. Make one per client or product: each
        shows only the monitors you choose, under the names you choose. Web addresses and error details are never shown.
      </p>
      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="success" role="status">{notice}</Alert>}
      {pages === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {pages !== null && pages.length === 0 && (
        <p className="hint">{isOwner ? 'You have no status page yet. Create one to share your uptime with your customers.' : 'There are no status pages yet.'}</p>
      )}
      {pages !== null && pages.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Status pages</caption>
            <thead>
              <tr><th>Title</th><th>Address</th><th>Visibility</th><th>Monitors</th><th>Actions</th></tr>
            </thead>
            <tbody>
              {pages.map((p) => (
                <tr key={p.id}>
                  <td>{p.title}</td>
                  <td>
                    {p.published ? (
                      <a href={`/status/${encodeURIComponent(p.slug)}`} target="_blank" rel="noreferrer">/status/{p.slug}</a>
                    ) : (
                      <span className="hint">/status/{p.slug}</span>
                    )}
                  </td>
                  <td>
                    {p.published
                      ? <span className="d-badge d-badge-soft d-badge-success">Public</span>
                      : <span className="d-badge d-badge-soft d-badge-ghost">Draft</span>}
                  </td>
                  <td>{p.monitors.length}</td>
                  <td className="actions"><Link to={`/status-pages/${p.id}`}>{isOwner ? 'Edit' : 'View'}</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {pages !== null && (
        <p className="hint">
          {pages.length} of {max} status page{max === 1 ? '' : 's'} used.
          {full && isOwner && (
            <> Your plan includes {max === 1 ? 'one status page' : `${max} status pages`}. <Link to="/billing">Upgrade</Link> to give each client or product its own.</>
          )}
        </p>
      )}
    </>
  )
}

/** The editor for one status page (or a new one). */
export function StatusPageEditor() {
  const { pageID } = useParams()
  const editing = Boolean(pageID)
  const navigate = useNavigate()
  const { organization } = useAuth()
  const isOwner = organization?.role === 'owner'

  const [loaded, setLoaded] = useState(false)
  const [monitors, setMonitors] = useState<Monitor[]>([])
  const [slug, setSlug] = useState('')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [published, setPublished] = useState(false)
  const [shown, setShown] = useState<Record<string, string | undefined>>({}) // monitor id -> display name ('' = own name); absent = not shown
  const [error, setError] = useState<string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  // Branding. The plan decides what may be changed; stored values are always sent back unchanged, so a page kept after a downgrade still saves.
  const [accent, setAccent] = useState('') // '' = the standard look
  const [website, setWebsite] = useState('')
  const [hidePoweredBy, setHidePoweredBy] = useState(false)
  const [hasLogo, setHasLogo] = useState(false)
  const [logoVersion, setLogoVersion] = useState(0)
  const [logoNotice, setLogoNotice] = useState<string | null>(null)
  const [canBrand, setCanBrand] = useState(false)
  const [canWhiteLabel, setCanWhiteLabel] = useState(false)

  const apply = (p: StatusPageConfig) => {
    setSlug(p.slug)
    setTitle(p.title)
    setDescription(p.description)
    setPublished(p.published)
    setShown(Object.fromEntries(p.monitors.map((x) => [x.monitor_id, x.display_name])))
    setAccent(p.accent_color ?? '')
    setWebsite(p.website_url ?? '')
    setHidePoweredBy(p.hide_powered_by ?? false)
    setHasLogo(p.has_logo ?? false)
    setLogoVersion(p.logo_version ?? 0)
  }

  const load = useCallback(async () => {
    try {
      const [p, m, u] = await Promise.all([
        pageID ? api<{ status_page: StatusPageConfig }>('GET', `/status-pages/${encodeURIComponent(pageID)}`) : Promise.resolve(null),
        api<{ monitors: Monitor[] }>('GET', '/monitors?limit=100'),
        api<{ usage: Usage }>('GET', '/usage').catch(() => null),
      ])
      setMonitors(m.monitors)
      setCanBrand(Boolean(u?.usage.plan.status_page_branding))
      setCanWhiteLabel(Boolean(u?.usage.plan.status_page_white_label))
      if (p) apply(p.status_page)
      setLoaded(true)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load the status page.')
    }
  }, [pageID])

  useEffect(() => {
    void load()
  }, [load])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setFields({})
    try {
      const ordered = monitors.filter((m) => m.id in shown)
      const body = {
        slug, title, description, published, monitors: ordered.map((m) => ({ monitor_id: m.id, display_name: shown[m.id] ?? '' })),
        accent_color: accent, website_url: website.trim(), hide_powered_by: hidePoweredBy,
      }
      if (pageID) await api('PUT', `/status-pages/${encodeURIComponent(pageID)}`, body)
      else await api('POST', '/status-pages', body)
      const notice = published ? `Saved. ${title} is public at /status/${slug}.` : `Saved. ${title} is a draft and is not visible to anyone yet.`
      navigate('/status-pages', { state: { notice } satisfies ListState })
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.fields).length > 0) setFields(err.fields)
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!pageID || !window.confirm(`Delete “${title}”? Its address will stop working.`)) return
    try {
      await api('DELETE', `/status-pages/${encodeURIComponent(pageID)}`)
      navigate('/status-pages', { state: { notice: `${title} was deleted.` } satisfies ListState })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    }
  }

  /** Uploads or removes the logo at once (it is not part of the form). */
  async function changeLogo(fn: () => Promise<{ status_page: StatusPageConfig }>, done: string) {
    setError(null)
    setLogoNotice(null)
    setFields({})
    try {
      const r = await fn()
      setHasLogo(r.status_page.has_logo)
      setLogoVersion(r.status_page.logo_version)
      setLogoNotice(done)
    } catch (err) {
      if (err instanceof ApiError && err.fields.logo) setFields({ logo: err.fields.logo })
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    }
  }

  // The address comes from a text field, so only link to it when it is a well-formed slug (what the server accepts), and encode it anyway.
  const linkable = /^[a-z0-9-]{3,40}$/.test(slug)
  const path = `/status/${encodeURIComponent(slug)}`
  const link = `${window.location.origin}${path}`

  return (
    <>
      <p className="hint"><Link to="/status-pages">← Status pages</Link></p>
      <div className="row">
        <PageIcon src={brand.icons.statusPage} />
        <h1>{editing ? title || 'Status page' : 'New status page'}</h1>
      </div>
      {error && <Alert>{error}</Alert>}
      {editing && loaded && published && linkable && (
        <p>
          Public at <a href={path} target="_blank" rel="noreferrer">{link}</a>
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
            <fieldset className="field">
              <legend>Branding</legend>
              {!canBrand && (
                <p className="hint">
                  A logo, an accent colour and a link back to your client’s website are included in the Pro and Agency plans. <Link to="/billing">Upgrade</Link>
                  {(accent || website || hasLogo) && ' Branding saved earlier is kept, but not shown on your current plan.'}
                </p>
              )}
              <fieldset disabled={!canBrand} className="plain">
                <div className="field">
                  <span>Logo</span>
                  {editing ? (
                    <>
                      {hasLogo && pageID && <img className="status-logo preview" src={`/api/v1/status-pages/${encodeURIComponent(pageID)}/logo?v=${logoVersion}`} alt="Current logo" />}
                      <input type="file" accept="image/png,image/jpeg" aria-label="Upload a logo (PNG or JPEG)"
                        onChange={(e) => {
                          const file = e.target.files?.[0]
                          e.target.value = ''
                          if (file && pageID) void changeLogo(() => apiUpload('PUT', `/status-pages/${encodeURIComponent(pageID)}/logo`, file), 'Logo saved.')
                        }} />
                      {hasLogo && pageID && (
                        <button type="button" className="linklike" onClick={() => void changeLogo(() => api('DELETE', `/status-pages/${encodeURIComponent(pageID)}/logo`), 'Logo removed.')}>
                          Remove logo
                        </button>
                      )}
                      {fields.logo && <small className="error">{fields.logo}</small>}
                      {logoNotice && <small className="hint" role="status">{logoNotice}</small>}
                      <small className="hint">PNG or JPEG, up to 512 KB. Shown at the top of the page. It is saved as soon as you choose it.</small>
                    </>
                  ) : (
                    <small className="hint">Create the page first, then add your client’s logo.</small>
                  )}
                </div>
                <label>
                  <input type="checkbox" checked={accent !== ''} onChange={(e) => setAccent(e.target.checked ? '#1a73e8' : '')} /> Use an accent colour for the header
                </label>
                {accent !== '' && (
                  <div className="field">
                    <label htmlFor="accent">Accent colour</label>
                    <input id="accent" type="color" value={accent} onChange={(e) => setAccent(e.target.value.toLowerCase())} />
                    {fields.accent_color && <small className="error">{fields.accent_color}</small>}
                    <div className="accent-preview" style={{ background: accent, color: readableTextOn(accent) }} aria-hidden="true">{title || 'Status'}</div>
                    <small className="hint">The header text is set to black or white automatically, whichever is easier to read on this colour.</small>
                  </div>
                )}
                <Field label="Link back to your client’s website (optional)" type="url" value={website} onChange={(e) => setWebsite(e.target.value)} error={fields.website_url}
                  placeholder="https://www.example.com" hint="Shown above the page title as “← Back to example.com”." />
              </fieldset>
              <label>
                <input type="checkbox" checked={hidePoweredBy} disabled={!canWhiteLabel && !hidePoweredBy} onChange={(e) => setHidePoweredBy(e.target.checked)} /> Hide “Powered by HawkEye”
              </label>
              {!canWhiteLabel && <small className="hint">Included in the Agency plan.</small>}
            </fieldset>
            <label>
              <input type="checkbox" className="d-toggle d-toggle-sm" checked={published} onChange={(e) => setPublished(e.target.checked)} /> Publish — anyone with the link can see this page
            </label>
          </fieldset>
          {isOwner ? (
            <p>
              <button type="submit" disabled={busy}>{editing ? 'Save status page' : 'Create status page'}</button>{' '}
              {editing && <button type="button" className="danger" onClick={() => void remove()}>Delete</button>}
            </p>
          ) : (
            <p className="hint">Only the organization owner can change status pages.</p>
          )}
        </form>
      )}
    </>
  )
}
