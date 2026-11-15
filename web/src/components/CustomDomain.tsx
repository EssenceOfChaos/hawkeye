import { useEffect, useState, type SubmitEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type StatusPageDomain, type StatusPageDomainState, type StatusPageDomainView } from '../api'
import { Alert } from './Alert'
import { CopyField } from './BadgeCard'
import { Field } from './Field'

// Full class names so the CSS generator can see them.
const STATE: Record<StatusPageDomainState, { label: string; cls: string; text: string }> = {
  pending: {
    label: 'Waiting for DNS records', cls: 'd-badge d-badge-soft d-badge-warning',
    text: 'Add the two records below at the company that runs DNS for this domain. HawkEye checks every minute for the first hour, then every 15 minutes for three days. DNS changes can take up to an hour to appear.',
  },
  active: {
    label: 'Live', cls: 'd-badge d-badge-soft d-badge-success',
    text: 'The status page is served on this domain, with a certificate HawkEye renews by itself. Leave both records in place: HawkEye checks them every day.',
  },
  failing: {
    label: 'Needs attention', cls: 'd-badge d-badge-soft d-badge-error',
    text: 'A DNS record has changed. The page is still served on this domain while you fix it, for up to 7 days.',
  },
  disabled: {
    label: 'Stopped', cls: 'd-badge d-badge-soft d-badge-ghost',
    text: 'The DNS records were wrong for 7 days, so the page is no longer served on this domain. Put the records back, then press Check now.',
  },
}

const when = (iso: string) => new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })

/**
 * A status page's custom domain (Agency): set it, add the two DNS records it asks for, and see whether it is live. Changes are saved at once, like the
 * logo, so this sits outside the page's own form. onChange tells the editor the domain's name and state (for the redirect switch).
 */
export function CustomDomainCard({ pageID, isOwner, onChange }: {
  pageID: string
  isOwner: boolean
  onChange?: (hostname: string, state: StatusPageDomainState | '') => void
}) {
  const [view, setView] = useState<StatusPageDomainView | null>(null)
  const [hostname, setHostname] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<string | undefined>()
  const [notice, setNotice] = useState<string | null>(null)
  const base = `/status-pages/${encodeURIComponent(pageID)}/domain`

  useEffect(() => {
    api<StatusPageDomainView>('GET', base).then(setView, (e: unknown) => setError(e instanceof ApiError ? e.message : 'Could not load the custom domain.'))
  }, [base])

  function show(v: StatusPageDomainView, done: string | null) {
    setView(v)
    setNotice(done)
    onChange?.(v.domain?.hostname ?? '', v.domain?.state ?? '')
  }

  async function run(fn: () => Promise<void>) {
    setBusy(true)
    setError(null)
    setFieldError(undefined)
    setNotice(null)
    try {
      await fn()
    } catch (err) {
      if (err instanceof ApiError && err.fields.hostname) setFieldError(err.fields.hostname)
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  const claim = (e: SubmitEvent) => {
    e.preventDefault()
    void run(async () => {
      const v = await api<StatusPageDomainView>('PUT', base, { hostname })
      setEditing(false)
      show(v, v.domain ? `Now add the two DNS records for ${v.domain.hostname}.` : null)
    })
  }
  const check = () => void run(async () => {
    const v = await api<StatusPageDomainView>('POST', `${base}/check`)
    // Otherwise the problem it found is shown in the card, with the time of the check.
    show(v, v.domain?.state === 'active' ? 'Both records are right: the page is live on this domain.' : null)
  })
  const remove = (d: StatusPageDomain) => {
    if (!window.confirm(`Stop serving this page on ${d.hostname}? You can set it up again later, with new DNS records.`)) return
    void run(async () => {
      await api('DELETE', base)
      show({ ...view!, domain: null }, `${d.hostname} was removed. You can delete its DNS records.`)
    })
  }

  if (error && !view) return <section className="card" aria-label="Custom domain"><h2>Custom domain</h2><Alert>{error}</Alert></section>
  if (!view) return null
  const d = view.domain
  const included = view.max > 0

  return (
    <section className="card custom-domain" aria-labelledby="custom-domain-title">
      <h2 id="custom-domain-title">Custom domain</h2>
      {!included && (
        <p className="hint">
          Serve this page at an address on your client’s own domain, such as status.yourclient.com, with a certificate HawkEye looks after. Included in
          the Agency plan. <Link to="/billing">Upgrade</Link>
          {d && ` ${d.hostname} is kept, but not served on your current plan.`}
        </p>
      )}
      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="success" role="status">{notice}</Alert>}

      {d && !editing && (
        <>
          <p className="domain-head">
            {d.state === 'active' || d.state === 'failing'
              ? <a href={`https://${d.hostname}/`} target="_blank" rel="noreferrer">{d.hostname}</a>
              : <strong>{d.hostname}</strong>}{' '}
            <span className={STATE[d.state].cls}>{STATE[d.state].label}</span>
          </p>
          {included && <p className="hint">{STATE[d.state].text}</p>}
          {d.last_error && d.state !== 'active' && <Alert tone={d.state === 'pending' ? 'info' : 'warning'}>{d.last_error}</Alert>}
          <fieldset className="plain">
            <legend>DNS records</legend>
            {d.records.map((r) => (
              <div key={r.type} className="dns-record">
                <p className="dns-record-type"><strong>{r.type}</strong>{r.type === 'CNAME' ? ' — sends visitors to HawkEye' : ' — proves the domain is yours'}</p>
                <CopyField label={`${r.type} name`} value={r.name} mono />
                <CopyField label={`${r.type} value`} value={r.value} mono />
              </div>
            ))}
            <small className="hint">
              Many DNS providers add the domain to a record’s name by themselves: if yours does, leave the domain off the end of each name.
              If your DNS provider offers a proxy (such as Cloudflare’s orange cloud), turn it off for the CNAME record.
            </small>
          </fieldset>
          {d.last_checked_at && <p className="hint">Last checked {when(d.last_checked_at)}.</p>}
          {isOwner && (
            <p className="row-actions">
              {included && <button type="button" disabled={busy} onClick={check}>{busy ? 'Checking…' : 'Check now'}</button>}{' '}
              <button type="button" className="linklike" disabled={busy} onClick={() => { setHostname(d.hostname); setEditing(true); setNotice(null) }}>Use another domain</button>{' '}
              <button type="button" className="linklike danger" disabled={busy} onClick={() => remove(d)}>Remove</button>
            </p>
          )}
        </>
      )}

      {isOwner && included && (!d || editing) && (
        <form onSubmit={claim}>
          <fieldset disabled={busy} className="plain">
            <Field label="Domain" required value={hostname} onChange={(e) => setHostname(e.target.value)} error={fieldError} placeholder="status.yourclient.com"
              maxLength={253} hint="A subdomain of a domain you or your client control, such as status.yourclient.com. A domain on its own (yourclient.com) can’t be used." />
            <p>
              <button type="submit">{d ? 'Use this domain' : 'Set up domain'}</button>{' '}
              {editing && <button type="button" className="linklike" onClick={() => { setEditing(false); setFieldError(undefined) }}>Cancel</button>}
            </p>
            {d && <small className="hint">The new domain needs new DNS records, and {d.hostname} stops being served at once.</small>}
          </fieldset>
        </form>
      )}
      {!isOwner && !d && included && <p className="hint">This page has no custom domain. Only the organization owner can set one up.</p>}
    </section>
  )
}
