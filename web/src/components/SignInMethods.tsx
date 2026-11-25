import { useState, type SubmitEvent } from 'react'
import { api, ApiError } from '../api'
import { needsConfirmation, type Reauth, PROVIDER_NAMES, PROVIDER_ORDER, startSocial, type Provider } from '../socialSignIn'
import { Alert } from './Alert'
import { Field } from './Field'
import { ProviderLogo } from './SocialButtons'
import { useConfirm } from './useConfirm'

/** How the signed-in person can sign in (GET /account/sign-in-methods). */
export interface SignInMethodsInfo {
  has_password: boolean
  identities: { provider: Provider; email: string; connected_at: string; last_used_at: string }[]
  /** The providers this server offers. */
  providers: Provider[]
}


/**
 * "Your password", or for an account without one, "Confirm with Google/GitHub/X": the provider sends the browser back to
 * /account?for=<purpose>, so the page reopens the step that asked. Reports whether the action can go ahead.
 */
export function PasswordOrConfirm({ reauth, purpose, password, onPassword, hint }: {
  reauth: Reauth
  purpose: string
  password: string
  onPassword: (v: string) => void
  hint?: string
}) {
  const [error, setError] = useState<string | null>(null)
  if (reauth.hasPassword) {
    return <Field label="Your password" type="password" autoComplete="current-password" required value={password} onChange={(e) => onPassword(e.target.value)} hint={hint} />
  }
  if (reauth.confirmed) {
    return <p className="hint" role="status">Confirmed with {PROVIDER_NAMES[reauth.confirmed]}. You have 10 minutes to finish.</p>
  }
  return (
    <div className="field">
      <p className="hint">Your account signs in with {reauth.connected.map((p) => PROVIDER_NAMES[p]).join(' or ')}, so confirm it’s you there first.</p>
      <div className="row">
        {reauth.connected.map((p) => (
          <button key={p} type="button" className={`social-btn social-btn-${p}`}
            onClick={() => void startSocial(p, 'confirm', `/account?for=${encodeURIComponent(purpose)}`).catch(() => setError('Could not reach the server. Try again.'))}>
            <ProviderLogo provider={p} /> <span>Confirm with {PROVIDER_NAMES[p]}</span>
          </button>
        ))}
      </div>
      {error && <Alert>{error}</Alert>}
    </div>
  )
}

const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })

/**
 * Settings → Sign-in methods: the password (set or not) and each provider this server offers, connected or not. Connecting goes through the provider
 * and comes back here; the last way to sign in can't be removed (the server refuses too).
 */
export function SignInMethodsCard({ info, reauth, notice, onChanged }: {
  info: SignInMethodsInfo
  reauth: Reauth
  /** A message from the provider's redirect back (connected, or why not). */
  notice: { tone: 'ok' | 'error'; text: string } | null
  onChanged: () => Promise<void>
}) {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)
  const ways = (info.has_password ? 1 : 0) + info.identities.length
  const shown = PROVIDER_ORDER.filter((p) => info.providers.includes(p) || info.identities.some((i) => i.provider === p))

  async function disconnect(p: Provider) {
    if (!(await confirm({ title: `Stop signing in with ${PROVIDER_NAMES[p]}?`, message: `This account will no longer sign in with ${PROVIDER_NAMES[p]}.`, confirmLabel: 'Disconnect', danger: true }))) return
    setBusy(true)
    setError(null)
    setDone(null)
    try {
      await api('DELETE', `/account/sign-in-methods/${p}`)
      await onChanged()
      setDone(`${PROVIDER_NAMES[p]} is disconnected.`)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  async function connect(p: Provider) {
    setError(null)
    try {
      await startSocial(p, 'connect')
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not reach the server. Try again.')
    }
  }

  return (
    <section className="card narrow-left" id="sign-in-methods" aria-labelledby="sim-h">
      <h2 id="sim-h">Sign-in methods</h2>
      <p className="hint">Use any of these to sign in. Email and password keep working alongside the others.</p>
      {notice && (notice.tone === 'ok' ? <Alert tone="success" role="status">{notice.text}</Alert> : <Alert>{notice.text}</Alert>)}
      {error && <Alert>{error}</Alert>}
      {done && <p role="status" className="hint">{done}</p>}
      <ul className="sign-in-methods">
        <li>
          <span className="sim-name">Email and password</span>
          <span className="hint">{info.has_password ? 'Set' : 'No password yet: set one below.'}</span>
        </li>
        {shown.map((p) => {
          const id = info.identities.find((i) => i.provider === p)
          return (
            <li key={p}>
              <span className="sim-name"><ProviderLogo provider={p} /> {PROVIDER_NAMES[p]}</span>
              {id ? (
                <>
                  <span className="hint">{id.email} · connected {day(id.connected_at)}</span>
                  <button type="button" className="linklike" disabled={busy || ways <= 1} onClick={() => void disconnect(p)}
                    title={ways <= 1 ? 'This is your only way to sign in. Set a password or connect another account first.' : undefined}>
                    Disconnect
                  </button>
                </>
              ) : info.providers.includes(p) ? (
                <button type="button" onClick={() => void connect(p)}>Connect {PROVIDER_NAMES[p]}</button>
              ) : null}
            </li>
          )
        })}
      </ul>
      {!reauth.hasPassword && <SetPassword reauth={reauth} onDone={onChanged} />}
      {dialog}
    </section>
  )
}

/** For an account created with Google, GitHub or X: add a password, after confirming it's you through the provider. */
function SetPassword({ reauth, onDone }: { reauth: Reauth; onDone: () => Promise<void> }) {
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (next !== confirm) {
      setError('The two passwords do not match.')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await api('POST', '/account/password', { password: next })
      await onDone()
    } catch (err) {
      if (needsConfirmation(err)) reauth.expire()
      setError(err instanceof ApiError ? (err.fields.password ?? err.message) : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit}>
      <h3>Set a password</h3>
      <PasswordOrConfirm reauth={reauth} purpose="password" password="" onPassword={() => undefined} />
      {reauth.confirmed && (
        <>
          <Field label="New password" type="password" autoComplete="new-password" required minLength={10} maxLength={128}
            hint="At least 10 characters." value={next} onChange={(e) => setNext(e.target.value)} />
          <Field label="Repeat new password" type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          <button type="submit" disabled={busy}>Set password</button>
        </>
      )}
      {error && <Alert>{error}</Alert>}
    </form>
  )
}
