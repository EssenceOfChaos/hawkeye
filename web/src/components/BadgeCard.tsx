import { useEffect, useState } from 'react'
import { api, ApiError, type Monitor, type Plan, type StatusPageConfig } from '../api'
import { Alert } from './Alert'
import { Appear } from './Appear'
import { Toggle } from './Toggle'
import { badgeLink, BADGE_SIZES as SIZES, snippets, type BadgeSize as Size } from '../badge'

/** A read-only value with a Copy button. */
export function CopyField({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false)
  const id = `copy-${label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <div className="kv-row">
        <input id={id} readOnly value={value} className={mono ? 'mono' : undefined} onFocus={(e) => e.target.select()} />
        <button type="button" onClick={() => {
          void navigator.clipboard?.writeText(value).then(() => {
            setCopied(true)
            setTimeout(() => setCopied(false), 2000)
          })
        }}>{copied ? 'Copied' : 'Copy'}</button>
      </div>
    </div>
  )
}

/** A monitor's public uptime badge: the owner turns it on, and anyone on the team can copy the code to embed it on a website or in a README. */
export function BadgeCard({ monitor, plan, isOwner }: { monitor: Monitor; plan: Plan | undefined; isOwner: boolean }) {
  const [token, setToken] = useState(monitor.badge_token)
  const [size, setSize] = useState<Size>('large')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pages, setPages] = useState<StatusPageConfig[]>([])
  const origin = window.location.origin

  useEffect(() => {
    if (!token) return
    api<{ status_pages: StatusPageConfig[] }>('GET', '/status-pages').then((r) => setPages(r.status_pages), () => undefined)
  }, [token])

  async function toggle(on: boolean): Promise<boolean> {
    if (!on && !window.confirm('Turn off the public badge? Anywhere it is embedded it will stop showing. Turning it on again gives it a new address.')) return true
    setBusy(true)
    setError(null)
    try {
      if (on) setToken((await api<{ badge_token: string }>('POST', `/monitors/${monitor.id}/badge`)).badge_token)
      else {
        await api('DELETE', `/monitors/${monitor.id}/badge`)
        setToken(null)
      }
      return true
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
      return false
    } finally {
      setBusy(false)
    }
  }

  const link = badgeLink(plan, origin, pages, monitor.id)
  const code = token ? snippets(origin, token, size, link) : null
  return (
    <section className="card" aria-labelledby="badge-h">
      <h2 id="badge-h">Uptime badge</h2>
      <p className="hint">
        Show this monitor’s uptime on a website or in a README. The badge shows only the uptime percentage, the period and whether it is up, never the
        monitor’s name or address, and it updates every few minutes.
      </p>
      {error && <Alert>{error}</Alert>}
      {isOwner && <Toggle checked={Boolean(token)} busy={busy} onToggle={toggle}>Public uptime badge</Toggle>}
      {!token && !isOwner && <p className="hint">The organization’s owner can turn on a public badge for this monitor.</p>}
      {token && code && (
        <Appear className="badge-options" play={token !== monitor.badge_token}>
          <fieldset className="field">
            <legend>Size</legend>
            {(Object.keys(SIZES) as Size[]).map((k) => (
              <label key={k}><input type="radio" name="badge-size" checked={size === k} onChange={() => setSize(k)} /> {SIZES[k].label}</label>
            ))}
          </fieldset>
          <p><img className="badge-preview" src={code.src} alt="Preview of the uptime badge" width={SIZES[size].width} height={SIZES[size].height} /></p>
          <CopyField label="HTML" value={code.html} />
          <CopyField label="Markdown" value={code.markdown} />
          <details className="explain">
            <summary>How do I add it?</summary>
            <p><strong>On a website:</strong> copy the HTML and paste it where the badge should appear, for example in your site’s footer.</p>
            <ul>
              <li><strong>WordPress:</strong> add a “Custom HTML” block and paste it in.</li>
              <li><strong>Squarespace:</strong> add a “Code” block. <strong>Wix:</strong> add “Embed code” → “Embed HTML”. <strong>Webflow:</strong> add an “Embed” element.</li>
              <li><strong>Shopify:</strong> in the theme editor, add a “Custom Liquid” section.</li>
            </ul>
            <p>
              <strong>In a README</strong> (GitHub, GitLab, Bitbucket or any Markdown file): choose the README size, copy the Markdown, paste it near the top of
              <code>README.md</code> and commit. GitHub shows images through its own cache, so a change can take a few minutes to appear there.
            </p>
            <p>
              <strong>Keeping it current:</strong> there is nothing to update. The badge refreshes itself every few minutes. If you turn it off, it disappears
              everywhere it is embedded; turning it on again gives it a new address, so paste the new code.
            </p>
          </details>
          <p className="hint">
            {plan?.code === 'free' || !plan
              ? 'On the Free plan the badge links to HawkEye.'
              : link
                ? 'The badge links to your public status page that shows this monitor.'
                : 'Add this monitor to a published status page and the badge will link to it.'}
          </p>
        </Appear>
      )}
    </section>
  )
}
