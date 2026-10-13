import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { BrandMark } from '../components/Brand'
import { Field } from '../components/Field'

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
  return <p role="alert" className="error">{error.message}</p>
}

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const { error, busy, run } = useSubmit()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    void run(async () => {
      await login(email, password)
      navigate('/dashboard')
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
      <h1>Create your free account</h1>
      <p>Monitor up to 5 websites for free. No credit card needed.</p>
      <form onSubmit={onSubmit}>
        <Field label="Email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} error={error?.fields.email} />
        <Field label="Password" type="password" autoComplete="new-password" required minLength={10} maxLength={128}
          hint="At least 10 characters." value={password} onChange={(e) => setPassword(e.target.value)} error={error?.fields.password} />
        <FormError error={error} />
        <button type="submit" disabled={busy}>Sign up</button>
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
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
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
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
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
      {state === 'failed' && <p role="alert" className="error">This link is invalid or has expired. Log in and request a new one.</p>}
    </section>
  )
}

/** Opened from the link in an alert-address confirmation email; the visitor may not have an account. */
export function ConfirmAlertAddressPage() {
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
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
      {state === 'failed' && <p role="alert" className="error">This link is invalid or has expired. Ask the person who added you to send a new confirmation.</p>}
    </section>
  )
}
