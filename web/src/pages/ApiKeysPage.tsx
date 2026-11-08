import { useCallback, useEffect, useState, type SubmitEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type ApiKey, type Usage } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { CopyField } from '../components/BadgeCard'
import { Field } from '../components/Field'

const day = (iso: string) => new Date(iso).toLocaleDateString([], { dateStyle: 'medium' })
const DAY_MS = 24 * 60 * 60 * 1000

type Expiry = '30' | '90' | '365' | 'custom' | 'never'

/** The yyyy-mm-dd of a date in local time, for a date input's min and max. */
function localDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** What the create request says about expiry: nothing (the 90-day default), a date, or no expiry. */
function expiryBody(choice: Expiry, custom: string): { expires_at?: string; no_expiry?: boolean } {
  if (choice === 'never') return { no_expiry: true }
  if (choice === 'custom') {
    const [y, m, d] = custom.split('-').map(Number)
    return { expires_at: new Date(y, m - 1, d, 23, 59, 0).toISOString() } // the end of the chosen day, in local time
  }
  if (choice === '90') return {}
  return { expires_at: new Date(Date.now() + Number(choice) * DAY_MS).toISOString() }
}

function Expires({ k, now }: { k: ApiKey; now: number }) {
  if (!k.expires_at) return <span className="hint">Never</span>
  if (k.expired) return <span className="error">Expired {day(k.expires_at)}</span>
  const left = Math.ceil((new Date(k.expires_at).getTime() - now) / DAY_MS)
  return (
    <>
      {day(k.expires_at)}
      {left <= 7 && <div className="hint">{left <= 1 ? 'Expires within a day' : `Expires in ${left} days`}</div>}
    </>
  )
}

/** API keys for the current organization: make one (shown once), see who made which, when each was last used and when it expires, and revoke. */
export function ApiKeysPage() {
  const { user, organization } = useAuth()
  const isOwner = organization?.role === 'owner'
  const verified = Boolean(user?.email_verified_at)

  const [keys, setKeys] = useState<ApiKey[] | null>(null)
  const [max, setMax] = useState(25)
  const [usage, setUsage] = useState<Usage | null>(null)
  const [error, setError] = useState<ApiError | string | null>(null)
  const [disabled, setDisabled] = useState(false)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [name, setName] = useState('')
  const [scope, setScope] = useState<'read' | 'write'>('read')
  const [expiry, setExpiry] = useState<Expiry>('90')
  const [custom, setCustom] = useState('')
  const [created, setCreated] = useState<{ key: ApiKey; token: string } | null>(null)
  const [now] = useState(() => Date.now()) // when the page opened: enough for "expires in N days" and the date picker's range

  const load = useCallback(async () => {
    try {
      const [r, u] = await Promise.all([api<{ keys: ApiKey[]; max_keys: number }>('GET', '/api-keys'), api<{ usage: Usage }>('GET', '/usage')])
      setKeys(r.keys)
      setMax(r.max_keys)
      setUsage(u.usage)
      setError(null)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'API_KEYS_DISABLED') setDisabled(true)
      else setError(e instanceof ApiError ? e.message : 'Could not load API keys.')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const canWrite = usage?.plan.api_write_enabled ?? false

  async function create(e: SubmitEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setFields({})
    setCreated(null)
    try {
      const r = await api<{ key: ApiKey; token: string }>('POST', '/api-keys', { name, scope, ...expiryBody(expiry, custom) })
      setCreated(r)
      setName('')
      await load()
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.fields).length > 0) setFields(err.fields)
      else setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function revoke(k: ApiKey) {
    const whose = k.mine ? '' : ` It belongs to ${k.owner_email}.`
    if (!window.confirm(`Revoke “${k.name}”? Anything using it stops working right away.${whose}`)) return
    try {
      await api('DELETE', `/api-keys/${k.id}`)
      if (created?.key.id === k.id) setCreated(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    }
  }

  const upgrade = error instanceof ApiError && error.code === 'PLAN_LIMIT_REACHED'

  return (
    <>
      <h1>API keys</h1>
      <p>
        An API key lets a script, dashboard or other tool use HawkEye’s API for {organization?.name ?? 'this organization'} without signing in. A key can do what you
        can do in this organization, and no more. Keep keys secret: anyone with one can use it.{' '}
        <Link to="/docs/api">How to use the API</Link>
      </p>

      {disabled && <Alert>API keys are turned off on this server right now. Keys you already have will work again once they are back on.</Alert>}
      {error && (
        <Alert>
          {typeof error === 'string' ? error : error.message} {upgrade && <Link to="/billing">See plans</Link>}
        </Alert>
      )}
      {keys === null && !error && !disabled && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {created && (
        <section className="card narrow-left" aria-labelledby="created-h">
          <h2 id="created-h">Copy your new key now</h2>
          <p>This is the only time HawkEye shows it. If you lose it, revoke it and create another.</p>
          <CopyField label={`API key “${created.key.name}”`} value={created.token} mono />
          <p className="hint">Send it in the Authorization header: <code>Authorization: Bearer {created.key.prefix}…</code></p>
          <button type="button" onClick={() => setCreated(null)}>I’ve copied it</button>
        </section>
      )}

      {keys && keys.length === 0 && <p className="hint">No API keys yet.</p>}
      {keys && keys.length > 0 && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">API keys</caption>
            <thead>
              <tr>
                <th>Name</th><th>Access</th><th>Key</th>{isOwner && <th>Made by</th>}<th>Last used</th><th>Expires</th><th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.id}>
                  <td>{k.name}<div className="hint">Created {day(k.created_at)}</div></td>
                  <td>{k.scope === 'write' ? 'Read and write' : 'Read only'}</td>
                  <td><code>{k.prefix}…</code></td>
                  {isOwner && <td>{k.mine ? 'You' : k.owner_email}</td>}
                  <td>{k.last_used_at ? day(k.last_used_at) : <span className="hint">Not used yet</span>}</td>
                  <td><Expires k={k} now={now} /></td>
                  <td className="actions">
                    <button className="danger" onClick={() => void revoke(k)} aria-label={`Revoke ${k.name}`}>Revoke</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {keys && isOwner && <p className="hint">You see every key in this organization because you own it, and you can revoke any of them. Up to {max} keys.</p>}
      {keys && !isOwner && <p className="hint">These are your keys. The organization’s owner can also see them and revoke them.</p>}

      {!disabled && keys && (
        verified ? (
          <section className="card narrow-left" aria-labelledby="new-h">
            <h2 id="new-h">Create a key</h2>
            <form onSubmit={create}>
              <Field label="Name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={fields.name}
                placeholder="Grafana dashboard" hint="So you can tell your keys apart later." />
              <fieldset className="field">
                <legend>Access</legend>
                <label>
                  <input type="radio" name="scope" value="read" checked={scope === 'read'} onChange={() => setScope('read')} /> Read only
                </label>
                <label>
                  <input type="radio" name="scope" value="write" checked={scope === 'write'} disabled={!canWrite} onChange={() => setScope('write')} /> Read and write
                </label>
                {!canWrite && usage && (
                  <small className="hint">
                    Your {usage.plan.name} plan can read through the API. <Link to="/billing">Upgrade</Link> to make changes with a key.
                  </small>
                )}
                {fields.scope && <small className="error">{fields.scope}</small>}
              </fieldset>
              <div className="field">
                <label htmlFor="expiry">Expires</label>
                <select id="expiry" value={expiry} onChange={(e) => setExpiry(e.target.value as Expiry)}>
                  <option value="30">In 30 days</option>
                  <option value="90">In 90 days (recommended)</option>
                  <option value="365">In 1 year</option>
                  <option value="custom">On a date I choose</option>
                  <option value="never">Never</option>
                </select>
                {expiry === 'never' && (
                  <small className="hint">A key that never expires keeps working until you revoke it. Use this for setups you can’t update often, and keep it safe.</small>
                )}
                {expiry !== 'never' && <small className="hint">HawkEye emails you a week before a key expires, and again when it does.</small>}
                {fields.expires_at && <small className="error">{fields.expires_at}</small>}
              </div>
              {expiry === 'custom' && (
                <Field label="Expiry date" type="date" required value={custom} onChange={(e) => setCustom(e.target.value)}
                  min={localDate(new Date(now + DAY_MS))} max={localDate(new Date(now + 365 * DAY_MS))} />
              )}
              <button type="submit" disabled={busy}>Create key</button>
            </form>
          </section>
        ) : (
          <p className="hint">Verify your email address first (see the link we sent you), so a key can be tied to an address you control.</p>
        )
      )}
    </>
  )
}
