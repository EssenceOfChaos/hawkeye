import { useCallback, useEffect, useState, type SubmitEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type TwoFactorStatus } from '../api'
import { useAuth } from '../auth'
import { Alert } from './Alert'
import { CopyField } from './BadgeCard'
import { Field } from './Field'

const day = (iso: string) => new Date(iso).toLocaleDateString([], { dateStyle: 'medium' })

function msg(e: unknown): string {
  return e instanceof ApiError ? e.message : 'That did not work. Try again.'
}

/** Recovery codes, shown once: copy or download them before closing. */
function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const text = codes.join('\n')
  function download() {
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([`HawkEye recovery codes\nEach code works once.\n\n${text}\n`], { type: 'text/plain' }))
    a.download = 'hawkeye-recovery-codes.txt'
    a.click()
    URL.revokeObjectURL(a.href)
  }
  return (
    <div role="status">
      <h3>Save your recovery codes</h3>
      <p>
        If you lose your phone, a recovery code gets you in instead of a code from the app. Each works once. Keep them somewhere safe, such as a password
        manager. This is the only time HawkEye shows them.
      </p>
      <ul className="recovery-codes mono">
        {codes.map((c) => <li key={c}>{c}</li>)}
      </ul>
      <div className="row">
        <button type="button" onClick={() => void navigator.clipboard?.writeText(text)}>Copy</button>
        <button type="button" onClick={download}>Download</button>
        <button type="button" className="primary" onClick={onDone}>I’ve saved them</button>
      </div>
    </div>
  )
}

/** Two-factor sign-in on the account page: set it up with an authenticator app, make new recovery codes, or turn it off. */
export function TwoFactorCard() {
  const { refresh } = useAuth()
  const [status, setStatus] = useState<TwoFactorStatus | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [step, setStep] = useState<'idle' | 'password' | 'scan' | 'codes' | 'regenerate' | 'disable'>('idle')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string; qr_code: string } | null>(null)
  const [codes, setCodes] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setStatus((await api<{ two_factor: TwoFactorStatus }>('GET', '/account/two-factor')).two_factor)
    } catch {
      setUnavailable(true) // the card is optional: if it can't load, leave it out rather than add an error to the page
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  function reset() {
    setStep('idle')
    setPassword('')
    setCode('')
    setSetup(null)
    setError(null)
  }

  async function act(fn: () => Promise<void>) {
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (e) {
      setError(msg(e))
    } finally {
      setBusy(false)
    }
  }

  const begin = (e: SubmitEvent) => {
    e.preventDefault()
    void act(async () => {
      const r = await api<{ setup: { secret: string; otpauth_uri: string; qr_code: string } }>('POST', '/account/two-factor/setup', { password })
      setSetup(r.setup)
      setPassword('')
      setStep('scan')
    })
  }
  const enable = (e: SubmitEvent) => {
    e.preventDefault()
    void act(async () => {
      const r = await api<{ recovery_codes: string[] }>('POST', '/account/two-factor/enable', { code })
      setCodes(r.recovery_codes)
      setCode('')
      setSetup(null)
      setStep('codes')
      await Promise.all([load(), refresh()])
    })
  }
  const regenerate = (e: SubmitEvent) => {
    e.preventDefault()
    void act(async () => {
      const r = await api<{ recovery_codes: string[] }>('POST', '/account/two-factor/recovery-codes', { code })
      setCodes(r.recovery_codes)
      setCode('')
      setStep('codes')
      await load()
    })
  }
  const disable = (e: SubmitEvent) => {
    e.preventDefault()
    void act(async () => {
      await api('POST', '/account/two-factor/disable', { password, code })
      reset()
      await Promise.all([load(), refresh()])
    })
  }

  if (unavailable) return null
  const codeField = (
    <Field label="Code from your authenticator app, or a recovery code" autoComplete="one-time-code" required value={code}
      onChange={(e) => setCode(e.target.value)} />
  )

  return (
    <section className="card narrow-left" id="two-factor" aria-labelledby="tf-h">
      <h2 id="tf-h">Two-factor sign-in</h2>
      {error && <Alert>{error}</Alert>}
      {status === null && <p role="status">Loading…</p>}

      {step === 'codes' && <RecoveryCodes codes={codes} onDone={reset} />}

      {status && !status.enabled && step !== 'codes' && (
        <>
          <p>
            Add a second step to signing in: after your password, HawkEye asks for a code from an authenticator app on your phone (such as 1Password, Google
            Authenticator, Microsoft Authenticator or Authy). Someone who learns your password still can’t get in.
          </p>
          {step === 'idle' && <button type="button" onClick={() => setStep('password')}>Set up two-factor sign-in</button>}
          {step === 'password' && (
            <form onSubmit={begin}>
              <Field label="Your password" type="password" autoComplete="current-password" required value={password}
                onChange={(e) => setPassword(e.target.value)} hint="To make sure it’s you." />
              <div className="row">
                <button type="submit" disabled={busy}>Continue</button>
                <button type="button" className="linklike" onClick={reset}>Cancel</button>
              </div>
            </form>
          )}
          {step === 'scan' && setup && (
            <form onSubmit={enable}>
              <p>1. Scan this QR code with your authenticator app.</p>
              <img src={setup.qr_code} alt="QR code to add HawkEye to an authenticator app" width={200} height={200} className="qr" />
              <details>
                <summary>Can’t scan it? Enter the key by hand</summary>
                <CopyField label="Setup key" value={setup.secret} mono />
              </details>
              <p>2. Enter the 6-digit code the app shows for HawkEye.</p>
              <Field label="Code" inputMode="numeric" autoComplete="one-time-code" maxLength={7} required value={code} onChange={(e) => setCode(e.target.value)} />
              <div className="row">
                <button type="submit" disabled={busy}>Turn on</button>
                <button type="button" className="linklike" onClick={reset}>Cancel</button>
              </div>
            </form>
          )}
        </>
      )}

      {status?.enabled && step !== 'codes' && (
        <>
          <p className="badge up"><span aria-hidden="true">✔</span> On since {status.enabled_at ? day(status.enabled_at) : 'recently'}</p>
          <p className={status.recovery_codes_left <= 3 ? 'warning' : 'hint'}>
            {status.recovery_codes_left} recovery code{status.recovery_codes_left === 1 ? '' : 's'} left.
            {status.recovery_codes_left <= 3 && ' Make new ones so you aren’t locked out if you lose your phone.'}
          </p>
          {step === 'idle' && (
            <div className="row">
              <button type="button" onClick={() => setStep('regenerate')}>Make new recovery codes</button>
              <button type="button" className="danger" onClick={() => setStep('disable')}>Turn off</button>
            </div>
          )}
          {step === 'regenerate' && (
            <form onSubmit={regenerate}>
              <p className="hint">Your old recovery codes stop working.</p>
              {codeField}
              <div className="row">
                <button type="submit" disabled={busy}>Make new codes</button>
                <button type="button" className="linklike" onClick={reset}>Cancel</button>
              </div>
            </form>
          )}
          {step === 'disable' && (
            <form onSubmit={disable}>
              <p>Signing in will need only your password again.</p>
              <Field label="Your password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
              {codeField}
              <div className="row">
                <button type="submit" className="danger" disabled={busy}>Turn off two-factor sign-in</button>
                <button type="button" className="linklike" onClick={reset}>Cancel</button>
              </div>
            </form>
          )}
        </>
      )}
    </section>
  )
}

interface MemberTwoFactor {
  user_id: string
  email: string
  two_factor_enabled: boolean
  grace_ends_at?: string
  blocked: boolean
}

/**
 * The owner's switch to require two-factor sign-in for everyone in the organization. Off unless the owner turns it on; they must use it themselves
 * first. Members who don't have it get a grace period and an email.
 */
export function RequireTwoFactorCard({ orgID }: { orgID: string }) {
  const { twoFactor } = useAuth()
  const [state, setState] = useState<{ required: boolean; grace_days: number; members: MemberTwoFactor[] } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [unavailable, setUnavailable] = useState(false)

  const load = useCallback(async () => {
    try {
      setState(await api('GET', `/organizations/${orgID}/two-factor`))
    } catch {
      setUnavailable(true) // optional card: left out if it can't load
    }
  }, [orgID])

  useEffect(() => {
    void load()
  }, [load])

  async function set(required: boolean) {
    const without = state?.members.filter((m) => !m.two_factor_enabled).length ?? 0
    if (required && without > 0 && !window.confirm(`${without} member${without === 1 ? ' doesn’t' : 's don’t'} use two-factor sign-in yet. They’ll be emailed and have ${state?.grace_days ?? 7} days to set it up before this organization is closed to them. Require it?`)) return
    setBusy(true)
    setError(null)
    try {
      setState(await api('PUT', `/organizations/${orgID}/two-factor`, { required }))
    } catch (e) {
      setError(msg(e))
    } finally {
      setBusy(false)
    }
  }

  if (unavailable || !state) return null
  const ownerHasIt = Boolean(twoFactor?.enabled)
  return (
    <section className="card narrow-left" aria-labelledby="req-tf-h">
      <h2 id="req-tf-h">Require two-factor sign-in</h2>
      <p>
        {state.required
          ? 'Everyone in this organization must use two-factor sign-in. Members who don’t are shut out (and so are their API keys) once their grace period ends, until they set it up.'
          : `Optional. When you require it, members who don’t use two-factor sign-in are emailed and have ${state.grace_days} days to set it up.`}
      </p>
      {error && <Alert>{error}</Alert>}
      {!state.required && !ownerHasIt && (
        <p className="hint">Turn on two-factor sign-in for your own account first (on your <Link to="/account#two-factor">account page</Link>), so you can’t be shut out.</p>
      )}
      <button type="button" disabled={busy || (!state.required && !ownerHasIt)} className={state.required ? undefined : 'primary'} onClick={() => void set(!state.required)}>
        {state.required ? 'Stop requiring it' : 'Require two-factor sign-in'}
      </button>
      {state.members.length > 1 && (
        <ul className="plain-list">
          {state.members.map((m) => (
            <li key={m.user_id}>
              {m.email}:{' '}
              {m.two_factor_enabled ? <span className="badge up">uses it</span>
                : m.blocked ? <span className="badge down">shut out until they set it up</span>
                : m.grace_ends_at ? <span className="badge slow">has until {day(m.grace_ends_at)}</span>
                : <span className="hint">doesn’t use it</span>}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/**
 * Tells a member when the organization they're looking at requires two-factor sign-in and they haven't set it up: a warning with the date during
 * the grace period, and a notice in place of the page once it's over.
 */
export function TwoFactorRequirementNotice() {
  const { organization, twoFactor } = useAuth()
  if (!organization?.require_two_factor || twoFactor?.enabled || !organization.two_factor_grace_ends_at) return null
  if (organization.two_factor_blocked) {
    return (
      <Alert>
        {organization.name} requires two-factor sign-in, so it’s closed to you until you set it up. <Link to="/account#two-factor">Set it up now</Link> (it
        takes a minute), or switch to another organization.
      </Alert>
    )
  }
  return (
    <Alert tone="warning" role="status">
      {organization.name} requires two-factor sign-in. Set it up by {day(organization.two_factor_grace_ends_at)} to keep access.{' '}
      <Link to="/account#two-factor">Set it up</Link>
    </Alert>
  )
}
