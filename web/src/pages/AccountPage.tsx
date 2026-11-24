import { useCallback, useEffect, useState, type SubmitEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { api, ApiError } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'
import { Appear } from '../components/Appear'
import { Toggle } from '../components/Toggle'
import { TwoFactorCard } from '../components/TwoFactor'
import { SecurityLog } from '../components/SecurityLog'
import { PasswordOrConfirm, SignInMethodsCard, type SignInMethodsInfo } from '../components/SignInMethods'
import { needsConfirmation, PROVIDER_NAMES, readSocialReturn, reauthReady, socialError, type Provider, type Reauth } from '../socialSignIn'

function msg(e: unknown): string {
  return e instanceof ApiError ? e.message : 'That did not work. Try again.'
}

function ChangePassword() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)
  const [busy, setBusy] = useState(false)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    setDone(false)
    setError(null)
    if (next !== confirm) {
      setErrors({ confirm: 'The two new passwords do not match.' })
      return
    }
    setErrors({})
    setBusy(true)
    try {
      await api('POST', '/auth/change-password', { current_password: current, new_password: next })
      setCurrent('')
      setNext('')
      setConfirm('')
      setDone(true)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'WRONG_PASSWORD') setErrors({ current: 'That is not your current password.' })
      else if (err instanceof ApiError && Object.keys(err.fields).length) setErrors({ next: err.fields.new_password ?? Object.values(err.fields)[0] })
      else setError(msg(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="card narrow-left" aria-labelledby="pw-h">
      <h2 id="pw-h">Change password</h2>
      <form onSubmit={submit}>
        <Field label="Current password" type="password" autoComplete="current-password" required value={current}
          onChange={(e) => setCurrent(e.target.value)} error={errors.current} />
        <Field label="New password" type="password" autoComplete="new-password" required minLength={10} maxLength={128}
          hint="At least 10 characters." value={next} onChange={(e) => setNext(e.target.value)} error={errors.next} />
        <Field label="Repeat new password" type="password" autoComplete="new-password" required value={confirm}
          onChange={(e) => setConfirm(e.target.value)} error={errors.confirm} />
        {error && <Alert>{error}</Alert>}
        {done && <p role="status">Password changed. Other devices were signed out.</p>}
        <button type="submit" disabled={busy}>Change password</button>
      </form>
    </section>
  )
}

function ExportData() {
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function download() {
    setBusy(true)
    setError(null)
    try {
      const res = await fetch('/api/v1/account/export', { credentials: 'same-origin' })
      if (!res.ok) {
        const body = await res.json().catch(() => null)
        throw new ApiError(res.status, body?.error?.code ?? 'UNKNOWN', body?.error?.message ?? 'Export failed.')
      }
      const url = URL.createObjectURL(await res.blob())
      const a = document.createElement('a')
      a.href = url
      a.download = 'hawkeye-export.json'
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (e) {
      setError(msg(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="card narrow-left" aria-labelledby="export-h">
      <h2 id="export-h">Export your data</h2>
      <p>Download your account details, organizations, monitors, incidents and alert recipients as a JSON file. Passwords and payment details are never included.</p>
      {error && <Alert>{error}</Alert>}
      <button onClick={() => void download()} disabled={busy}>{busy ? 'Preparing…' : 'Download my data'}</button>
    </section>
  )
}

function DeleteAccount({ email, reauth }: { email: string; reauth: Reauth }) {
  const { refresh } = useAuth()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const matches = typed.trim().toLowerCase() === email.toLowerCase()

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (!window.confirm('Permanently delete your account and all of its data? This cannot be undone.')) return
    setBusy(true)
    setError(null)
    try {
      await api('POST', '/account/delete', { password, confirm_email: typed })
      await refresh()
      navigate('/', { replace: true })
    } catch (err) {
      if (needsConfirmation(err)) reauth.expire()
      setError(msg(err))
      setBusy(false)
    }
  }

  return (
    <section className="card narrow-left danger-zone" aria-labelledby="del-h">
      <h2 id="del-h">Delete account</h2>
      <p>
        This permanently deletes your account, your organization, all monitors, check history, incidents and alert recipients. If you have a paid
        plan, the subscription is <strong>canceled immediately and is not refunded</strong>. This cannot be undone — consider exporting your data first.
      </p>
      <form onSubmit={submit}>
        <PasswordOrConfirm reauth={reauth} purpose="delete" password={password} onPassword={setPassword} />
        <Field label={`Type ${email} to confirm`} value={typed} autoComplete="off" required onChange={(e) => setTyped(e.target.value)} />
        {error && <Alert>{error}</Alert>}
        <button type="submit" className="danger" disabled={busy || !matches || !reauthReady(reauth, password)}>Delete my account</button>
      </form>
    </section>
  )
}

interface EmailPreferences {
  weekly_summary: boolean
  onboarding: boolean
}

/** The optional emails the person receives. Alerts, verification and security email are not optional, so they are not here. */
function EmailPreferencesCard() {
  const [prefs, setPrefs] = useState<EmailPreferences | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState<string | null>(null)

  useEffect(() => {
    api<{ email_preferences: EmailPreferences }>('GET', '/account/email-preferences').then((r) => setPrefs(r.email_preferences), (e: unknown) => setError(msg(e)))
  }, [])

  // The switch moves at once, and goes back if the server refuses.
  async function change(key: keyof EmailPreferences, on: boolean, done: string): Promise<boolean> {
    setError(null)
    setSaved(null)
    setPrefs((p) => p && { ...p, [key]: on })
    try {
      const r = await api<{ email_preferences: EmailPreferences }>('PUT', '/account/email-preferences', { [key]: on })
      setPrefs(r.email_preferences)
      setSaved(done)
      return true
    } catch (e) {
      setPrefs((p) => p && { ...p, [key]: !on })
      setError(msg(e))
      return false
    }
  }

  return (
    <section className="card narrow-left" aria-labelledby="mail-h">
      <h2 id="mail-h">Email preferences</h2>
      {error && <Alert>{error}</Alert>}
      {prefs && (
        <>
          <Toggle checked={prefs.weekly_summary} onToggle={(on) => change('weekly_summary', on, on ? 'Weekly summary is on.' : 'Weekly summary is off.')}>Weekly summary</Toggle>
          <p className="hint">Every Monday: last week’s uptime, incidents and slowest sites, and certificates and domains about to expire, for each team you belong to.</p>
          <Toggle checked={prefs.onboarding} onToggle={(on) => change('onboarding', on, on ? 'Getting-started tips are on.' : 'Getting-started tips are off.')}>Getting-started tips</Toggle>
          <p className="hint">A few emails in your first days, only until you add your first website.</p>
          {saved && <Appear><p className="hint" role="status">{saved}</p></Appear>}
          <p className="hint">Alerts about your websites, and account and security emails, are always sent.</p>
        </>
      )}
    </section>
  )
}

/**
 * What the provider's redirect back to this page says (read once, then taken out of the address bar): a provider connected or why not
 * (?connected=, ?social_error=), and a confirmation for a sensitive action (#confirmed=google, with ?for= naming the step that asked).
 */
function useSocialReturn() {
  const location = useLocation()
  const navigate = useNavigate()
  const [r] = useState(() => {
    const q = new URLSearchParams(location.search)
    const connected = q.get('connected') as Provider | null
    const err = socialError(q.get('social_error'))
    let notice: { tone: 'ok' | 'error'; text: string } | null = null
    if (err) notice = { tone: 'error', text: err }
    else if (connected && connected in PROVIDER_NAMES) notice = { tone: 'ok', text: `${PROVIDER_NAMES[connected]} is connected. You can use it to sign in.` }
    return { notice, confirmed: readSocialReturn(location.hash).confirmed ?? null, purpose: q.get('for'), focus: notice ? 'sign-in-methods' : q.get('for') }
  })
  useEffect(() => {
    if (location.search || (location.hash && location.hash !== '#sign-in-methods')) navigate(location.pathname, { replace: true })
    const id = r.focus === 'delete' ? 'del-h' : r.focus?.startsWith('two-factor') ? 'two-factor' : r.focus ? 'sign-in-methods' : null
    if (id) document.getElementById(id)?.scrollIntoView?.()
  }, [location.hash, location.pathname, location.search, navigate, r.focus])
  return r
}

export function AccountPage() {
  const { user } = useAuth()
  const [resent, setResent] = useState(false)
  const back = useSocialReturn()
  const [methods, setMethods] = useState<SignInMethodsInfo | null>(null)
  const [confirmed, setConfirmed] = useState<Provider | null>(back.confirmed)
  const loadMethods = useCallback(async () => {
    try {
      setMethods(await api<SignInMethodsInfo>('GET', '/account/sign-in-methods'))
    } catch {
      // Without the list, the page works as before: a password is asked for where needed.
    }
  }, [])
  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- loadMethods() sets state only after its request returns, never synchronously
    void loadMethods()
  }, [loadMethods])
  if (!user) return null

  const reauth: Reauth = {
    hasPassword: methods?.has_password ?? true,
    connected: methods?.identities.map((i) => i.provider) ?? [],
    confirmed,
    expire: () => setConfirmed(null),
  }

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.settings} />
        <h1>Account</h1>
      </div>

      <section className="card narrow-left" aria-labelledby="profile-h">
        <h2 id="profile-h">Profile</h2>
        <p><strong>{user.email}</strong></p>
        {user.email_verified_at ? (
          <p className="badge up"><span aria-hidden="true">✔</span> Email verified</p>
        ) : (
          <p>
            <span className="badge unknown"><span aria-hidden="true">⏳</span> Email not verified</span>{' '}
            {resent ? 'A new link is on its way.' : (
              <button className="linklike" onClick={() => void api('POST', '/auth/resend-verification').then(() => setResent(true))}>Resend verification email</button>
            )}
          </p>
        )}
      </section>

      {methods && (methods.providers.length > 0 || methods.identities.length > 0) && (
        <SignInMethodsCard info={methods} reauth={reauth} notice={back.notice} onChanged={loadMethods} />
      )}
      <EmailPreferencesCard />
      {reauth.hasPassword && <ChangePassword />}
      <TwoFactorCard reauth={reauth} resume={back.purpose} />
      <section className="card" aria-labelledby="activity-h">
        <h2 id="activity-h">Recent activity</h2>
        <p className="hint">
          Sign-ins to your account, including failed ones, and changes to your password and two-factor sign-in. If you see something you don’t recognize,
          change your password and turn on two-factor sign-in.
        </p>
        <SecurityLog path="/account/security-log" pageSize={10} caption="Recent activity on your account" />
      </section>
      <ExportData />
      <DeleteAccount email={user.email} reauth={reauth} />
    </>
  )
}
