import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { api, ApiError } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

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

  async function submit(e: FormEvent) {
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

function DeleteAccount({ email }: { email: string }) {
  const { refresh } = useAuth()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const matches = typed.trim().toLowerCase() === email.toLowerCase()

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!window.confirm('Permanently delete your account and all of its data? This cannot be undone.')) return
    setBusy(true)
    setError(null)
    try {
      await api('POST', '/account/delete', { password, confirm_email: typed })
      await refresh()
      navigate('/', { replace: true })
    } catch (err) {
      setError(msg(err))
      setBusy(false)
    }
  }

  return (
    <section className="card narrow-left danger-zone" aria-labelledby="del-h">
      <h2 id="del-h">Delete account</h2>
      <p>
        This permanently deletes your account, your organization, all monitors, check history, incidents and alert recipients. If you have a paid
        plan, the subscription is <strong>cancelled immediately and is not refunded</strong>. This cannot be undone — consider exporting your data first.
      </p>
      <form onSubmit={submit}>
        <Field label="Your password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        <Field label={`Type ${email} to confirm`} value={typed} autoComplete="off" required onChange={(e) => setTyped(e.target.value)} />
        {error && <Alert>{error}</Alert>}
        <button type="submit" className="danger" disabled={busy || !matches || password === ''}>Delete my account</button>
      </form>
    </section>
  )
}

export function AccountPage() {
  const { user } = useAuth()
  const [resent, setResent] = useState(false)
  if (!user) return null

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

      <ChangePassword />
      <ExportData />
      <DeleteAccount email={user.email} />
    </>
  )
}
