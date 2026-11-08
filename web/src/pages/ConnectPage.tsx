import { useEffect, useMemo, useState, type SubmitEvent } from 'react'
import { useLocation } from 'react-router'
import { api, ApiError } from '../api'
import { Alert } from '../components/Alert'
import { BrandMark } from '../components/Brand'
import { Field } from '../components/Field'

interface ConsentOrganization {
  id: string
  name: string
  role: 'owner' | 'member'
  can_write: boolean
  blocked: boolean
}

interface ConsentInfo {
  client: { name: string }
  requests_write: boolean
  organizations: ConsentOrganization[]
  two_factor_code_needed: boolean
  deny_redirect: string
  error_redirect?: string
  /** Where the browser goes back to afterward, so the person can see it is the app they expect. */
  redirect_host?: string
  /** That address is on this computer only: any program could be listening there. */
  redirect_loopback?: boolean
}

/** Sends the browser to the app that asked (its registered redirect address, checked by the server). Kept apart so tests can watch it. */
export const leave = { to: (url: string) => window.location.assign(url) }

/**
 * The consent page for connecting an app (such as Claude) through OAuth: /oauth/authorize sends people here with the request's parameters. They see
 * which app is asking, choose the organization and the access it gets, confirm with a two-factor code if they use it, and are sent back to the app.
 */
export function ConnectPage() {
  const { search } = useLocation()
  const params = useMemo(() => Object.fromEntries(new URLSearchParams(search)), [search])
  const [info, setInfo] = useState<ConsentInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [orgID, setOrgID] = useState('')
  const [access, setAccess] = useState<'read' | 'write'>('read')
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api<ConsentInfo>('GET', `/oauth/authorize${search}`).then(
      (r) => {
        if (r.error_redirect) {
          leave.to(r.error_redirect)
          return
        }
        setInfo(r)
        const first = r.organizations.find((o) => !o.blocked)
        if (first) setOrgID(first.id)
      },
      (e) => setError(e instanceof ApiError ? e.message : 'This connection request could not be loaded.'),
    )
  }, [search])

  const org = info?.organizations.find((o) => o.id === orgID)
  const canWrite = Boolean(info?.requests_write && org?.can_write)

  async function allow(e: SubmitEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ redirect: string }>('POST', '/oauth/authorize', {
        ...params, organization_id: orgID, access: canWrite ? access : 'read', two_factor_code: code || undefined,
      })
      leave.to(r.redirect)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
      setBusy(false)
    }
  }

  if (!info) {
    return (
      <section className="card narrow">
        <BrandMark />
        {error ? <Alert>{error}</Alert> : <p role="status">Loading…</p>}
      </section>
    )
  }
  const name = info.client.name
  const usable = info.organizations.filter((o) => !o.blocked)
  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Connect {name} to HawkEye</h1>
      <p>
        {name} wants to use HawkEye as you. It will see the monitors, uptime, incidents, status pages and maintenance windows of the organization you
        choose{info.requests_write ? ', and can make changes there if you allow it' : ''}. It never sees your password, billing, team or alert settings.
      </p>
      {usable.length === 0 ? (
        <Alert>You can’t connect apps to any of your organizations right now. Set up two-factor sign-in on your account page first.</Alert>
      ) : (
        <form onSubmit={allow}>
          <div className="field">
            <label htmlFor="org">Organization</label>
            <select id="org" value={orgID} onChange={(e) => setOrgID(e.target.value)}>
              {info.organizations.map((o) => (
                <option key={o.id} value={o.id} disabled={o.blocked}>
                  {o.name}{o.role === 'member' ? ' (member)' : ''}{o.blocked ? ' (needs two-factor sign-in)' : ''}
                </option>
              ))}
            </select>
          </div>
          <fieldset className="field">
            <legend>Access</legend>
            <label>
              <input type="radio" name="access" checked={!canWrite || access === 'read'} onChange={() => setAccess('read')} /> Read only
            </label>
            {info.requests_write && (
              <label>
                <input type="radio" name="access" checked={canWrite && access === 'write'} disabled={!canWrite} onChange={() => setAccess('write')} />{' '}
                Read and make changes (pause and add monitors, post incident updates, schedule maintenance)
              </label>
            )}
            {info.requests_write && !org?.can_write && (
              <small className="hint">{org?.name}’s plan can read through the API but not make changes. Upgrade to Pro or Agency to allow changes.</small>
            )}
          </fieldset>
          {info.two_factor_code_needed && (
            <Field label="Code from your authenticator app" inputMode="numeric" autoComplete="one-time-code" required value={code}
              onChange={(e) => setCode(e.target.value)} hint="To confirm it’s you connecting this app." />
          )}
          {error && <Alert>{error}</Alert>}
          <div className="row">
            <button type="submit" className="primary" disabled={busy || !orgID}>Allow</button>
            <button type="button" disabled={busy} onClick={() => leave.to(info.deny_redirect)}>Deny</button>
          </div>
          {info.redirect_host && (
            <p className="hint">After you choose, you’ll go back to <strong>{info.redirect_host}</strong>.</p>
          )}
          {info.redirect_loopback && (
            <Alert tone="warning" role="status">
              This app runs on your own computer. Only allow it if you just started connecting {name} yourself.
            </Alert>
          )}
          <p className="hint">You can disconnect {name} at any time on the API keys page. HawkEye emails you when an app is connected.</p>
        </form>
      )}
    </section>
  )
}
