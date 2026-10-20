import { useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { Field } from '../components/Field'
import { brand } from '../brand'
import { CookieSettingsButton } from '../components/CookieBanner'

interface Category {
  code: string
  label: string
  hint: string
  priority: string
}

const MAX_MESSAGE = 5000
// Topics where the website in question helps support reproduce the problem.
const WANTS_URL = new Set(['technical', 'bug', 'other'])

/** Public contact form. The chosen topic is the triage: it tags the support email and sets its priority. */
export function ContactPage() {
  const { user } = useAuth()
  const [categories, setCategories] = useState<Category[] | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [category, setCategory] = useState('')
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [subject, setSubject] = useState('')
  const [message, setMessage] = useState('')
  const [url, setUrl] = useState('')
  const [company, setCompany] = useState('') // honeypot: real visitors never see or fill this
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const [reference, setReference] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    api<{ categories: Category[] }>('GET', '/contact/categories')
      .then((r) => !cancelled && setCategories(r.categories))
      .catch(() => !cancelled && setLoadFailed(true))
    return () => {
      cancelled = true
    }
  }, [])

  // Signed-in visitors start with their own address; they can still change it.
  useEffect(() => {
    if (user?.email) setEmail((current) => current || user.email)
  }, [user?.email])

  const fields = error?.fields ?? {}
  const selected = categories?.find((c) => c.code === category)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ reference: string }>('POST', '/contact', {
        category, name, email, subject, message, company,
        url: WANTS_URL.has(category) ? url : '',
      })
      setReference(r.reference)
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, 'UNKNOWN', 'Something went wrong. Please try again.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="legal contact">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logoWhite} alt="HawkEye" width={160} height={32} /></Link>
        <span className="spacer" />
        {user ? <Link to="/dashboard">Back to dashboard</Link> : <Link to="/login">Log in</Link>}
      </header>
      <main>
        <h1>Contact us</h1>

        {reference ? (
          <section className="card" role="status">
            <h2>Thanks, your message is on its way</h2>
            <p>
              Your reference is <strong className="mono">{reference}</strong>. We’ve sent a confirmation to <strong>{email}</strong> and will reply to that address.
            </p>
            <p className="hint">Can’t find the confirmation? Check your spam folder, and quote the reference if you write to us again.</p>
            <p><Link to={user ? '/dashboard' : '/'}>{user ? 'Back to the dashboard' : 'Back to the home page'}</Link></p>
          </section>
        ) : (
          <>
            <p>Tell us what you need and we’ll route your message to the right place. Choose the topic that fits best.</p>
            {loadFailed && <Alert>The contact form could not be loaded. Please try again in a moment.</Alert>}
            {!categories && !loadFailed && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}
            {categories && (
              <form onSubmit={onSubmit} noValidate>
                <fieldset className="topics">
                  <legend>What is this about?</legend>
                  <div className="topic-grid">
                    {categories.map((c) => (
                      <label key={c.code} className={`topic${category === c.code ? ' selected' : ''}`}>
                        <input type="radio" name="category" value={c.code} checked={category === c.code} onChange={() => setCategory(c.code)} />
                        <span className="topic-label">{c.label}</span>
                        <span className="hint">{c.hint}</span>
                      </label>
                    ))}
                  </div>
                  {fields.category && <small className="error" role="alert">{fields.category}</small>}
                </fieldset>

                {category === 'billing' && <p className="hint">For billing questions, writing from the email address on your account helps us find it quickly.</p>}
                {category === 'account' && <p className="hint">For data export or deletion you can also use the self-service options on your <Link to="/account">account page</Link> when signed in.</p>}

                <Field label="Your name" autoComplete="name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} error={fields.name} />
                <Field label="Email address" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)}
                  hint="We’ll reply here." error={fields.email} />
                <Field label="Subject" required maxLength={150} value={subject} onChange={(e) => setSubject(e.target.value)} error={fields.subject} />
                {WANTS_URL.has(category) && (
                  <Field label="Website concerned (optional)" type="url" inputMode="url" placeholder="https://example.com" value={url} onChange={(e) => setUrl(e.target.value)}
                    hint="The address of the website this is about, if any." error={fields.url} />
                )}
                <div className="field">
                  <label htmlFor="contact-message">Message</label>
                  <textarea id="contact-message" required rows={8} maxLength={MAX_MESSAGE} value={message} onChange={(e) => setMessage(e.target.value)}
                    aria-invalid={fields.message ? true : undefined} aria-describedby="contact-message-count" />
                  <small id="contact-message-count" className={fields.message ? 'error' : 'hint'}>
                    {fields.message ?? `${message.length} / ${MAX_MESSAGE} characters`}
                  </small>
                </div>

                {/* Honeypot: hidden from people and assistive technology; bots that fill every field give themselves away. */}
                <div className="hp" aria-hidden="true">
                  <label>Company<input tabIndex={-1} autoComplete="off" value={company} onChange={(e) => setCompany(e.target.value)} /></label>
                </div>

                {error && !Object.keys(fields).length && <Alert>{error.message}</Alert>}
                <button type="submit" className="primary" disabled={busy}>{busy ? 'Sending…' : 'Send message'}</button>
                {selected && <p className="hint">Topic: {selected.label}</p>}
              </form>
            )}
          </>
        )}
        <p className="hint"><Link to="/privacy">Privacy policy</Link> · <Link to="/terms">Terms of service</Link> · <CookieSettingsButton /> · <Link to="/">Home</Link></p>
      </main>
    </div>
  )
}
