import { useId, useState, type SubmitEvent } from 'react'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { Reveal } from './Reveal'

/**
 * Features that are coming soon, each with "Get notified": a waitlist whose sign-up counts show which to build first (internal/waitlist). Keep the
 * keys in step with waitlist.Features and the migration's CHECK. These are not plan features: they never appear on the pricing cards until built.
 */
export const COMING_SOON = [
  {
    key: 'status_page_domains',
    title: 'Status pages on your own domain',
    text: 'Serve each status page at an address like status.yourbrand.com, with a certificate HawkEye looks after, so clients see your brand, not ours.',
  },
  {
    key: 'status_page_subscribers',
    title: 'Email updates for status page visitors',
    text: 'Let customers subscribe to a status page and get an email when something goes down and when it’s fixed, without you sending a thing.',
  },
] as const

type Key = (typeof COMING_SOON)[number]['key']

function NotifyMe({ feature, title }: { feature: Key; title: string }) {
  const { user } = useAuth()
  const id = useId()
  const verified = Boolean(user?.email_verified_at)
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState('')
  const [company, setCompany] = useState('') // honeypot: real visitors never see or fill this
  const [state, setState] = useState<'idle' | 'busy' | 'added' | 'already' | 'check_email'>('idle')
  const [error, setError] = useState<string | null>(null)

  async function join(e?: SubmitEvent) {
    e?.preventDefault()
    setState('busy')
    setError(null)
    try {
      const r = await api<{ status: 'added' | 'already' | 'check_email' }>('POST', '/waitlist', { feature, email: verified ? undefined : email, company })
      setState(r.status)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
      setState('idle')
    }
  }

  if (state === 'added' || state === 'already') {
    return <p className="hint" role="status">{state === 'added' ? 'You’re on the list.' : 'You’re already on the list.'} We’ll email {user?.email} when it’s ready.</p>
  }
  if (state === 'check_email') {
    return <p className="hint" role="status">Check your inbox: click the link in our email to confirm, and we’ll tell you when it’s ready.</p>
  }
  // Signed in with a verified address: one click, no form.
  if (verified) {
    return <button type="button" disabled={state === 'busy'} onClick={() => void join()} aria-label={`Get notified about ${title}`}>Get notified</button>
  }
  if (!open) {
    return <button type="button" onClick={() => setOpen(true)} aria-label={`Get notified about ${title}`}>Get notified</button>
  }
  return (
    <form className="notify-form" onSubmit={join}>
      <label htmlFor={id} className="sr-only">Your email address</label>
      <input id={id} type="email" autoComplete="email" required placeholder="you@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
      {/* Honeypot: hidden from people and assistive technology; bots that fill every field give themselves away. */}
      <div className="hp" aria-hidden="true">
        <label>Company<input tabIndex={-1} autoComplete="off" value={company} onChange={(e) => setCompany(e.target.value)} /></label>
      </div>
      <button type="submit" disabled={state === 'busy'}>Notify me</button>
      {error && <small className="error" role="alert">{error}</small>}
      <small className="hint">One email when it launches, nothing else. You’ll confirm your address first.</small>
    </form>
  )
}

/** The landing page's "Coming soon" section. */
export function ComingSoon() {
  return (
    <section aria-labelledby="soon-h" className="section">
      <h2 id="soon-h">Coming soon</h2>
      <p>What we’re building next. Tell us which you want, and we’ll let you know the moment it’s ready.</p>
      <Reveal as="ul" className="features coming-soon">
        {COMING_SOON.map((f) => (
          <li key={f.key} className="feature" data-reveal>
            <span className="d-badge d-badge-soft d-badge-warning">Coming soon</span>
            <h3>{f.title}</h3>
            <p>{f.text}</p>
            <NotifyMe feature={f.key} title={f.title} />
          </li>
        ))}
      </Reveal>
    </section>
  )
}
