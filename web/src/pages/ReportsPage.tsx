import { useState, type ChangeEvent, type FormEvent } from 'react'
import { Link } from 'react-router'
import { api, apiUpload, ApiError, type BrandingView, type Monitor, type Usage } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'
import { reportMonths, reportPdfPath } from '../format'
import { useApi } from '../hooks'

const MAX_LOGO_BYTES = 512 * 1024

export function ReportsPage() {
  const { organization } = useAuth()
  const isOwner = organization?.role === 'owner'

  const usage = useApi<{ usage: Usage }>('/usage')
  const monitors = useApi<{ monitors: Monitor[] }>('/monitors?limit=100')
  const loaded = useApi<BrandingView>('/branding')
  const [view, setView] = useState<BrandingView | null>(null)
  const current = view ?? loaded.data

  const [month, setMonth] = useState<string | null>(null)
  const [name, setName] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [error, setError] = useState<ApiError | string | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [logoVersion, setLogoVersion] = useState(0)

  const months = reportMonths(usage.data?.usage.plan.history_retention_days ?? 7)
  // Only a month this page offered is used: the selected value is looked up in our own list, never passed on as typed.
  const chosen = months.find((x) => x.value === month)?.value ?? months[1]?.value ?? months[0].value
  const canReport = current?.can_report ?? false
  const canBrand = current?.can_brand ?? false
  const shownName = name ?? current?.branding.display_name ?? ''

  async function change<T extends BrandingView>(run: () => Promise<T>, done: string) {
    setBusy(true)
    setError(null)
    setFields({})
    setNotice(null)
    try {
      setView(await run())
      setNotice(done)
      return true
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length > 0) setFields(e.fields)
      else setError(e instanceof ApiError ? e : 'That did not work. Try again.')
      return false
    } finally {
      setBusy(false)
    }
  }

  function saveName(e: FormEvent) {
    e.preventDefault()
    void change(() => api<BrandingView>('PUT', '/branding', { display_name: shownName }), 'Saved.').then((ok) => ok && setName(null))
  }

  function toggleMonthly(e: ChangeEvent<HTMLInputElement>) {
    const on = e.target.checked
    void change(() => api<BrandingView>('PUT', '/branding', { monthly_report_email: on }), on ? 'Monthly emails are on.' : 'Monthly emails are off.')
  }

  function uploadLogo(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    if (file.size > MAX_LOGO_BYTES) {
      setFields({ logo: 'is too large: use an image under 512 KB' })
      return
    }
    void change(() => apiUpload<BrandingView>('PUT', '/branding/logo', file), 'Logo saved.').then((ok) => ok && setLogoVersion((v) => v + 1))
  }

  function removeLogo() {
    void change(() => api<BrandingView>('DELETE', '/branding/logo'), 'Logo removed.')
  }

  const upgrade = error instanceof ApiError && error.code === 'PLAN_LIMIT_REACHED'

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.reports} />
        <h1>Reports</h1>
      </div>
      <p>
        A monthly PDF for each website: uptime, confirmed downtime, a day-by-day chart and every incident. Send it to the client the website belongs to.
      </p>

      {error && (
        <Alert>
          {typeof error === 'string' ? error : error.message} {upgrade && <Link to="/billing">See plans</Link>}
        </Alert>
      )}
      {notice && <Alert tone="success">{notice}</Alert>}
      {loaded.error && !current && <Alert>{loaded.error.message}</Alert>}

      <section className="card" aria-labelledby="dl-h">
        <h2 id="dl-h">Download a report</h2>
        {current && !canReport && (
          <Alert tone="info">PDF reports are included in the Pro and Agency plans. <Link to="/billing">See plans</Link></Alert>
        )}
        {current && canReport && (
          <>
            <div className="field">
              <label htmlFor="report-month">Month</label>
              <select id="report-month" value={chosen} onChange={(e) => setMonth(e.target.value)}>
                {months.map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
              </select>
              <small className="hint">Months are calendar months in UTC. Only months your plan still keeps history for are listed.</small>
            </div>
            {monitors.data && monitors.data.monitors.length === 0 && <p>Add a website first, and its report will appear here.</p>}
            {monitors.data && monitors.data.monitors.length > 0 && (
              <ul className="plain">
                {monitors.data.monitors.map((m) => (
                  <li key={m.id} className="row">
                    <span>{m.name}</span>
                    <span className="spacer" />
                    <a className="button" href={reportPdfPath(m.id, chosen)} rel="noreferrer">Download PDF</a>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </section>

      <section className="card" aria-labelledby="mail-h">
        <h2 id="mail-h">Monthly email</h2>
        <p>On the 1st of each month, HawkEye emails last month’s reports, one PDF per website, to the owners of this workspace. You forward each one to your client.</p>
        {current && !canReport && <Alert tone="info">Monthly report emails are included in the Pro and Agency plans. <Link to="/billing">See plans</Link></Alert>}
        <label className="check">
          <input
            type="checkbox"
            checked={current?.branding.monthly_report_email ?? false}
            disabled={!current || !canReport || !isOwner || busy}
            onChange={toggleMonthly}
          />
          <span>Email me the reports every month</span>
        </label>
        {current && canReport && !isOwner && <small className="hint">Only an owner of the workspace can change this.</small>}
      </section>

      <section className="card" aria-labelledby="brand-h">
        <h2 id="brand-h">Your branding</h2>
        <p>Put your own name and logo on the reports, so they look like they come from you.</p>
        {current && !canBrand && (
          <Alert tone="info">Reports with your own name and logo are included in the Agency plan. Other plans get the HawkEye look. <Link to="/billing">See plans</Link></Alert>
        )}
        <form onSubmit={saveName}>
          <Field
            label="Name on reports"
            value={shownName}
            maxLength={100}
            disabled={!current || !canBrand || !isOwner}
            error={fields.display_name}
            hint="Shown at the top and in the footer, for example your agency’s name."
            onChange={(e) => setName(e.target.value)}
          />
          <button type="submit" disabled={busy || !canBrand || !isOwner || name === null}>Save name</button>
        </form>

        <div className="field">
          <label htmlFor="logo-file">Logo</label>
          {current?.branding.has_logo && (
            <p>
              <img
                className="logo-preview"
                src={`/api/v1/branding/logo?v=${logoVersion}`}
                alt={`Your logo (${current.branding.logo_width} × ${current.branding.logo_height} pixels)`}
                style={{ maxWidth: 240, maxHeight: 80, background: '#fff', padding: 8, borderRadius: 6 }}
              />
            </p>
          )}
          <input id="logo-file" type="file" accept="image/png,image/jpeg" disabled={busy || !canBrand || !isOwner} onChange={uploadLogo} aria-describedby="logo-hint" />
          <small id="logo-hint" className="hint">PNG or JPEG, up to 512 KB and 2000 × 2000 pixels. A wide logo on a transparent or white background works best.</small>
          {fields.logo && <small className="error">{`Logo ${fields.logo}`}</small>}
          {current?.branding.has_logo && (
            <p><button type="button" onClick={removeLogo} disabled={busy || !isOwner}>Remove logo</button></p>
          )}
        </div>
        {current && !isOwner && <small className="hint">Only an owner of the workspace can change branding.</small>}
      </section>
    </>
  )
}
