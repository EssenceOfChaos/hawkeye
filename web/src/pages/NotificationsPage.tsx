import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, ApiError, type NotificationSetting } from '../api'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

type Channel = NotificationSetting['channel']

const channelNames: Record<Channel, string> = { email: 'Email', slack: 'Slack', webhook: 'Webhook' }

function StatusLabel({ s }: { s: NotificationSetting }) {
  if (!s.verified) return <span className="badge unknown"><span aria-hidden="true">⏳</span> Waiting for confirmation</span>
  if (!s.enabled) return <span className="badge paused"><span aria-hidden="true">⏸</span> Alerts off</span>
  return <span className="badge up"><span aria-hidden="true">✔</span> Receiving alerts</span>
}

export function NotificationsPage() {
  const { user, organizations } = useAuth()
  const isOwner = organizations[0]?.role === 'owner'
  const verified = Boolean(user?.email_verified_at)

  const [items, setItems] = useState<NotificationSetting[] | null>(null)
  const [max, setMax] = useState(10)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [channel, setChannel] = useState<Channel>('email')
  const [address, setAddress] = useState('')
  const [secret, setSecret] = useState<string | null>(null)
  const [fieldError, setFieldError] = useState<string | undefined>()
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const r = await api<{ notification_settings: NotificationSetting[]; max: number }>('GET', '/notification-settings')
      setItems(r.notification_settings)
      setMax(r.max)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load alert recipients.')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function run(fn: () => Promise<unknown>, success?: string) {
    setError(null)
    setNotice(null)
    setSecret(null)
    try {
      await fn()
      if (success) setNotice(success)
      await load()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
    }
  }

  async function add(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setFieldError(undefined)
    setError(null)
    setNotice(null)
    setSecret(null)
    try {
      const r = await api<{ notification_setting: NotificationSetting; signing_secret?: string }>('POST', '/notification-settings', { channel, destination: address })
      setAddress('')
      setSecret(r.signing_secret ?? null)
      setNotice(
        channel !== 'email'
          ? `Added. We sent a test message and this ${channelNames[channel]} destination will receive alerts.`
          : r.notification_setting.verified
            ? 'Added. This address will receive alerts.'
            : `We emailed ${r.notification_setting.destination} a confirmation link. It will receive alerts once confirmed.`,
      )
      await load()
    } catch (err) {
      if (err instanceof ApiError && err.fields.destination) setFieldError(err.fields.destination)
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  function remove(s: NotificationSetting) {
    if (window.confirm(`Stop sending alerts to ${s.destination} and remove it?`)) {
      void run(() => api('DELETE', `/notification-settings/${s.id}`))
    }
  }

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.email} />
        <h1>Alert recipients</h1>
      </div>
      <p>Everything listed here is told when a monitor is confirmed down, when it recovers, and when a certificate is about to expire: by email, in Slack, or by a signed webhook to your own systems.</p>

      {!verified && (
        <p role="status" className="banner inline">
          Verify your own email address first — until then you can’t add other recipients, and your own address won’t receive alerts.
        </p>
      )}
      {error && <p role="alert" className="error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {secret && (
        <p role="status" className="banner inline">
          Signing secret: <code>{secret}</code>. Copy it now — it is not shown again. HawkEye signs every request with it (HMAC-SHA256 in the <code>X-HawkEye-Signature</code> header), so your server can check a request is genuine.
        </p>
      )}
      {items === null && !error && <p role="status">Loading…</p>}

      {items && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Alert recipients</caption>
            <thead>
              <tr><th>Destination</th><th>Status</th>{isOwner && <th>Actions</th>}</tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <tr key={s.id}>
                  <td>
                    {s.channel !== 'email' && <span className="hint">{channelNames[s.channel]}: </span>}
                    {s.destination}
                    {s.user_id && <span className="hint"> (you)</span>}
                  </td>
                  <td><StatusLabel s={s} /></td>
                  {isOwner && (
                    <td className="actions">
                      {s.verified && s.channel !== 'email' && (
                        <button onClick={() => void run(() => api('POST', `/notification-settings/${s.id}/test`), 'Test message sent.')}>Send test</button>
                      )}
                      {s.verified ? (
                        <button onClick={() => void run(() => api('PATCH', `/notification-settings/${s.id}`, { enabled: !s.enabled }))}>
                          {s.enabled ? 'Turn off' : 'Turn on'}
                        </button>
                      ) : (
                        verified && (
                          <button onClick={() => void run(() => api('POST', `/notification-settings/${s.id}/resend-confirmation`), 'Confirmation email sent.')}>
                            Resend confirmation
                          </button>
                        )
                      )}
                      <button className="danger" onClick={() => remove(s)} aria-label={`Remove ${s.destination}`}>Remove</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {isOwner ? (
        <section className="card narrow-left" aria-labelledby="add-h">
          <h2 id="add-h">Add a recipient</h2>
          <form onSubmit={add}>
            <div className="field">
              <label htmlFor="channel">Channel</label>
              <select id="channel" value={channel} onChange={(e) => { setChannel(e.target.value as Channel); setFieldError(undefined) }}>
                <option value="email">Email</option>
                <option value="slack">Slack</option>
                <option value="webhook">Webhook</option>
              </select>
            </div>
            {channel === 'email' ? (
              <Field label="Email address" type="email" required value={address} onChange={(e) => setAddress(e.target.value)}
                error={fieldError} disabled={!verified || (items?.length ?? 0) >= max}
                hint="New addresses must confirm via an emailed link before they receive alerts." />
            ) : (
              <Field label={channel === 'slack' ? 'Slack incoming webhook URL' : 'Webhook URL'} type="url" required value={address} onChange={(e) => setAddress(e.target.value)}
                error={fieldError} disabled={!verified || (items?.length ?? 0) >= max} placeholder={channel === 'slack' ? 'https://hooks.slack.com/services/…' : 'https://example.com/hooks/hawkeye'}
                hint={channel === 'slack' ? 'Create one in Slack under Apps → Incoming Webhooks. We send a test message first.' : 'HTTPS only. We send a signed test message first, and every alert is a signed JSON POST.'} />
            )}
            <button type="submit" disabled={busy || !verified || (items?.length ?? 0) >= max}>{channel === 'email' ? 'Add recipient' : 'Add destination'}</button>
            {items && items.length >= max && <p className="hint">You have reached the limit of {max} recipients.</p>}
          </form>
        </section>
      ) : (
        <p className="hint">Only the organization owner can change alert recipients.</p>
      )}
    </>
  )
}
