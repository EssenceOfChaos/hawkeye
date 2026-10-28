import { useEffect, useState, type FormEvent } from 'react'
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router'
import { api, ApiError } from '../api'
import { afterLoginPath } from '../navigation'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { BrandMark } from '../components/Brand'
import { Field } from '../components/Field'

/**
 * Reads the one-time `?token=` from the URL and immediately removes it from the address bar (history entry replaced), so it does not linger in
 * browser history, screen shares, bookmarks or later Referer headers. The value is kept in component state for the API call.
 */
function useTokenFromUrl(): string {
  const [params] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const [token] = useState(() => params.get('token') ?? '')
  useEffect(() => {
    if (new URLSearchParams(location.search).has('token')) navigate(location.pathname, { replace: true })
  }, [location.pathname, location.search, navigate])
  return token
}

function useSubmit() {
  const [error, setError] = useState<ApiError | null>(null)
  const [busy, setBusy] = useState(false)
  async function run(fn: () => Promise<void>) {
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setBusy(false)
    }
  }
  return { error, busy, run }
}

function FormError({ error }: { error: ApiError | null }) {
  if (!error || Object.keys(error.fields).length) return null
  return <Alert>{error.message}</Alert>
}

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const { error, busy, run } = useSubmit()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    void run(async () => {
      await login(email, password)
      navigate(afterLoginPath(location.state))
    })
  }
  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Log in</h1>
      <form onSubmit={onSubmit}>
        <Field label="Email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <Field label="Password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        <FormError error={error} />
        <button type="submit" disabled={busy}>Log in</button>
      </form>
      <p><Link to="/forgot-password">Forgot your password?</Link></p>
      <p>No account? <Link to="/register">Sign up free</Link></p>
    </section>
  )
}

export function RegisterPage() {
  const { register } = useAuth()
  const navigate = useNavigate()
  const { error, busy, run } = useSubmit()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    void run(async () => {
      await register(email, password)
      navigate('/dashboard')
    })
  }
  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Start with a clearer view</h1>
      <p>Create your free HawkEye account and bring your website monitoring into one place. Monitor up to 5 websites free; no credit card needed.</p>
      <p className="hint">By creating an account you agree to the <Link to="/terms">Terms</Link> and the <Link to="/privacy">Privacy policy</Link>.</p>
      <form onSubmit={onSubmit}>
        <Field label="Email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} error={error?.fields.email} />
        <Field label="Password" type="password" autoComplete="new-password" required minLength={10} maxLength={128}
          hint="At least 10 characters." value={password} onChange={(e) => setPassword(e.target.value)} error={error?.fields.password} />
        <FormError error={error} />
        <button type="submit" disabled={busy}>Create free account</button>
      </form>
      <p>Already registered? <Link to="/login">Log in</Link></p>
    </section>
  )
}

export function ForgotPasswordPage() {
  const { error, busy, run } = useSubmit()
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    void run(async () => {
      await api('POST', '/auth/forgot-password', { email })
      setSent(true)
    })
  }
  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Reset your password</h1>
      {sent ? (
        <p role="status">If an account exists for that address, a reset link is on its way.</p>
      ) : (
        <form onSubmit={onSubmit}>
          <Field label="Email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          <FormError error={error} />
          <button type="submit" disabled={busy}>Send reset link</button>
        </form>
      )}
      <p><Link to="/login">Back to log in</Link></p>
    </section>
  )
}

export function ResetPasswordPage() {
  const token = useTokenFromUrl()
  const { error, busy, run } = useSubmit()
  const [password, setPassword] = useState('')
  const [done, setDone] = useState(false)

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    void run(async () => {
      await api('POST', '/auth/reset-password', { token, password })
      setDone(true)
    })
  }
  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Choose a new password</h1>
      {done ? (
        <p role="status">Password updated. <Link to="/login">Log in</Link></p>
      ) : (
        <form onSubmit={onSubmit}>
          <Field label="New password" type="password" autoComplete="new-password" required minLength={10} maxLength={128}
            hint="At least 10 characters." value={password} onChange={(e) => setPassword(e.target.value)} error={error?.fields.password} />
          <FormError error={error} />
          <button type="submit" disabled={busy || !token}>Update password</button>
        </form>
      )}
    </section>
  )
}

export function VerifyEmailPage() {
  const token = useTokenFromUrl()
  const { refresh } = useAuth()
  const [state, setState] = useState<'working' | 'ok' | 'failed'>('working')

  useEffect(() => {
    let cancelled = false
    api('POST', '/auth/verify-email', { token })
      .then(() => {
        if (!cancelled) setState('ok')
        void refresh()
      })
      .catch(() => !cancelled && setState('failed'))
    return () => {
      cancelled = true
    }
  }, [token, refresh])

  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Email verification</h1>
      {state === 'working' && <p role="status">Verifying…</p>}
      {state === 'ok' && <p role="status">Your email address is verified. <Link to="/monitors">Continue</Link></p>}
      {state === 'failed' && <Alert>This link is invalid or has expired. Log in and request a new one.</Alert>}
    </section>
  )
}

/** Opened from the link in an alert-address confirmation email; the visitor may not have an account. */
export function ConfirmAlertAddressPage() {
  const token = useTokenFromUrl()
  const [state, setState] = useState<'working' | 'ok' | 'failed'>('working')

  useEffect(() => {
    let cancelled = false
    api('POST', '/notification-settings/confirm', { token })
      .then(() => !cancelled && setState('ok'))
      .catch(() => !cancelled && setState('failed'))
    return () => {
      cancelled = true
    }
  }, [token])

  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Alert address confirmation</h1>
      {state === 'working' && <p role="status">Confirming…</p>}
      {state === 'ok' && <p role="status">Thanks — this address will now receive HawkEye alerts. You can close this page.</p>}
      {state === 'failed' && <Alert>This link is invalid or has expired. Ask the person who added you to send a new confirmation.</Alert>}
    </section>
  )
}

/**
 * Opened from the footer of an onboarding email; the visitor need not be signed in. It asks before it acts: some mail systems open every link in a
 * message to scan it, and that must never unsubscribe anyone. (A mail client's own "Unsubscribe" button uses the API directly.)
 */
export function UnsubscribePage() {
  const token = useTokenFromUrl()
  const [state, setState] = useState<'ask' | 'working' | 'ok' | 'failed'>(token ? 'ask' : 'failed')

  async function unsubscribe() {
    setState('working')
    try {
      await api('POST', `/lifecycle/unsubscribe?token=${encodeURIComponent(token)}`)
      setState('ok')
    } catch {
      setState('failed')
    }
  }

  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Stop these emails?</h1>
      {(state === 'ask' || state === 'working') && (
        <>
          <p>You will stop receiving getting-started emails from HawkEye. Alerts about your monitors, and account and security emails, are not affected.</p>
          <button type="button" onClick={() => void unsubscribe()} disabled={state === 'working'}>Stop these emails</button>
        </>
      )}
      {state === 'ok' && <p role="status">Done. We won't send you any more getting-started emails.</p>}
      {state === 'failed' && <Alert>This link is not valid. Please <Link to="/contact">contact us</Link> and we will stop them for you.</Alert>}
    </section>
  )
}

/**
 * Opened from the link in an invitation email. It says which team the invitation is for before anything happens, then joins it once the visitor is
 * signed in with the invited address. A visitor who is not signed in is sent to log in or create an account and brought back here; the link's token
 * rides along in the router's history state (never the address bar, where it would linger), and is read from the address again on the way back.
 */
export function AcceptInvitePage() {
  const token = useTokenFromUrl()
  const { status, user, logout, refresh } = useAuth()
  const navigate = useNavigate()
  const [invite, setInvite] = useState<{ organization_name: string; email: string } | null>(null)
  const [loaded, setLoaded] = useState(!token)
  const [error, setError] = useState<ApiError | string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!token) return
    let live = true
    api<{ invitation: { organization_name: string; email: string } }>('POST', '/invitations/preview', { token })
      .then((r) => live && setInvite(r.invitation))
      .catch(() => undefined)
      .finally(() => live && setLoaded(true))
    return () => {
      live = false
    }
  }, [token])

  async function accept() {
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ organization: { id: string } }>('POST', '/invitations/accept', { token })
      await refresh(r.organization.id)
      navigate('/dashboard', { replace: true })
    } catch (e) {
      setError(e instanceof ApiError ? e : 'That did not work. Try again.')
      setBusy(false)
    }
  }

  const back = { from: `/accept-invite?token=${encodeURIComponent(token)}` }
  const wrongAccount = invite && user && user.email.toLowerCase() !== invite.email.toLowerCase()

  return (
    <section className="card narrow">
      <BrandMark />
      <h1>Join a team on HawkEye</h1>
      {!loaded || status === 'loading' ? (
        <p role="status">Loading…</p>
      ) : !invite ? (
        <Alert>This invitation link is not valid, or it has expired. Ask the person who invited you to send a new one.</Alert>
      ) : (
        <>
          <p>
            You have been invited to join <strong>{invite.organization_name}</strong> as <strong>{invite.email}</strong>.
          </p>
          {error && <Alert>{typeof error === 'string' ? error : error.message}</Alert>}
          {status === 'anonymous' ? (
            <>
              <p>Log in, or create a free account, using <strong>{invite.email}</strong> to accept.</p>
              <p className="row">
                <Link className="button primary" to="/login" state={back}>Log in</Link>
                <Link className="button" to="/register" state={back}>Create account</Link>
              </p>
            </>
          ) : wrongAccount ? (
            <>
              <Alert tone="warning">You are signed in as {user.email}, but this invitation is for {invite.email}.</Alert>
              <button type="button" onClick={() => void logout()}>Log out and use {invite.email}</button>
            </>
          ) : (
            <>
              {user && !user.email_verified_at && (
                <Alert tone="warning">Confirm your email address first, using the link we emailed you. Then come back to this page and accept.</Alert>
              )}
              <button type="button" onClick={() => void accept()} disabled={busy}>{busy ? 'Joining…' : 'Accept invitation'}</button>
            </>
          )}
        </>
      )}
    </section>
  )
}
