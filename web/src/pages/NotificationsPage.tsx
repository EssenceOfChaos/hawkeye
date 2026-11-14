import { useCallback, useEffect, useRef, useState, type SubmitEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type AlertCoverage, type NotificationSetting, type TagSummary, type TelegramLink } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'
import { TagChip } from '../components/Tags'

type Channel = NotificationSetting['channel']

const channelNames: Record<Channel, string> = { email: 'Email', slack: 'Slack', discord: 'Discord', teams: 'Microsoft Teams', telegram: 'Telegram', pagerduty: 'PagerDuty', webhook: 'Webhook' }

/** How to ask for each channel that takes a URL. */
const urlFields: Record<'slack' | 'discord' | 'teams' | 'webhook', { label: string; placeholder: string; hint: string }> = {
  slack: {
    label: 'Slack incoming webhook URL',
    placeholder: 'https://hooks.slack.com/services/…',
    hint: 'Create one in Slack under Apps → Incoming Webhooks. We send a test message first.',
  },
  discord: {
    label: 'Discord webhook URL',
    placeholder: 'https://discord.com/api/webhooks/…',
    hint: 'In Discord, open the channel’s settings → Integrations → Webhooks → New Webhook, then Copy Webhook URL. We send a test message first.',
  },
  teams: {
    label: 'Teams workflow URL',
    placeholder: 'https://….logic.azure.com/workflows/…',
    hint: 'In Teams, open the channel’s ⋯ menu → Workflows → “Post to a channel when a webhook request is received”, and copy the URL it gives you. We send a test message first.',
  },
  webhook: {
    label: 'Webhook URL',
    placeholder: 'https://example.com/hooks/hawkeye',
    hint: 'HTTPS only. We send a signed test message first, and every alert is a signed JSON POST.',
  },
}

const TELEGRAM_POLL_MS = 3000

/**
 * Connects a Telegram chat through HawkEye's bot: a one-time link opens Telegram, someone taps Start (or picks a group), and the chat appears
 * in the list by itself. No bot token or chat ID is ever shown.
 */
function TelegramConnect({ bot, disabled, items, onConnected, onError }: {
  bot: string
  disabled: boolean
  items: NotificationSetting[] | null
  onConnected: (s: NotificationSetting) => void
  onError: (message: string | null) => void
}) {
  const [link, setLink] = useState<TelegramLink | null>(null)
  const [busy, setBusy] = useState(false)
  const known = useRef<Set<string>>(new Set()) // recipients that existed before the link was made

  async function start() {
    setBusy(true)
    onError(null)
    try {
      const r = await api<{ telegram_link: TelegramLink }>('POST', '/notification-settings/telegram-link')
      known.current = new Set((items ?? []).map((s) => s.id))
      setLink(r.telegram_link)
    } catch (e) {
      onError(e instanceof ApiError ? e.message : 'Could not start connecting Telegram. Try again.')
    } finally {
      setBusy(false)
    }
  }

  // Wait for the chat to appear; the link works once and expires.
  useEffect(() => {
    if (!link) return
    const until = new Date(link.expires_at).getTime()
    const timer = window.setInterval(() => {
      if (Date.now() > until) {
        setLink(null)
        onError('That Telegram link has expired. Click Connect Telegram to get a new one.')
        return
      }
      api<{ notification_settings: NotificationSetting[] }>('GET', '/notification-settings').then((r) => {
        const added = r.notification_settings.find((s) => s.channel === 'telegram' && !known.current.has(s.id))
        if (added) {
          setLink(null)
          onConnected(added)
        }
      }, () => undefined) // a missed poll is retried by the next one
    }, TELEGRAM_POLL_MS)
    return () => window.clearInterval(timer)
  }, [link, onConnected, onError])

  if (!link) {
    return (
      <div className="field">
        <p>Alerts come from our bot, <strong>@{bot}</strong>. No tokens or chat IDs needed.</p>
        <button type="button" disabled={disabled || busy} onClick={() => void start()}>{busy ? 'Starting…' : 'Connect Telegram'}</button>
      </div>
    )
  }
  return (
    <div className="field" aria-live="polite">
      <p><strong>Open Telegram and tap Start.</strong> This page updates by itself once the chat is connected.</p>
      <p className="row">
        <a className="button primary" href={link.chat_url} target="_blank" rel="noopener noreferrer">Send alerts to me</a>
        <a className="button" href={link.group_url} target="_blank" rel="noopener noreferrer">Add to a group</a>
      </p>
      <p className="hint">
        For a channel: <a href={link.channel_url} target="_blank" rel="noopener noreferrer">add @{link.bot} as an admin</a> that can post messages,
        then post <code>/connect {link.code}</code> in the channel.
      </p>
      <p className="hint" role="status">
        <span className="d-loading d-loading-spinner d-loading-xs" aria-hidden="true" /> Waiting for Telegram… The link works once and expires in 15 minutes.{' '}
        <button type="button" className="linklike" onClick={() => setLink(null)}>Cancel</button>
      </p>
    </div>
  )
}

/** Which monitors a destination is told about: all of them, or the tags it is routed to. */
export function RoutingSummary({ s, tags }: { s: NotificationSetting; tags: TagSummary[] }) {
  if (s.monitor_scope !== 'tags') return <span>All monitors</span>
  const chosen = tags.filter((t) => s.tag_ids.includes(t.id))
  if (chosen.length === 0) return <span className="hint">No monitors (its tags were deleted)</span>
  return (
    <ul className="tag-list" aria-label="Only monitors tagged">
      {chosen.map((t) => <li key={t.id}><TagChip tag={t} /></li>)}
    </ul>
  )
}

/** Owner-only: choose whether a destination gets every monitor's alerts or only those of monitors with some tags. */
export function RoutingEditor({ s, tags, onSave, onCancel }: {
  s: NotificationSetting
  tags: TagSummary[]
  onSave: (scope: 'all' | 'tags', tagIDs: string[]) => void
  onCancel: () => void
}) {
  const [scope, setScope] = useState<'all' | 'tags'>(s.monitor_scope)
  const [chosen, setChosen] = useState<string[]>(s.tag_ids.filter((id) => tags.some((t) => t.id === id)))
  const name = `scope-${s.id}`
  return (
    <fieldset className="group routing-editor">
      <legend>Send alerts for</legend>
      <label><input type="radio" name={name} checked={scope === 'all'} onChange={() => setScope('all')} /> All monitors, and account-wide alerts such as domain expiry</label>
      <label>
        <input type="radio" name={name} checked={scope === 'tags'} disabled={tags.length === 0} onChange={() => setScope('tags')} /> Only monitors with these tags
      </label>
      {tags.length === 0 && <p className="hint">Create tags on the <Link to="/monitors">Monitors</Link> page to send a client only their own sites’ alerts.</p>}
      {scope === 'tags' && (
        <div className="filters" role="group" aria-label="Tags">
          {tags.map((t) => {
            const on = chosen.includes(t.id)
            return (
              <label key={t.id} className={`chip${on ? ' selected' : ''}`}>
                <input type="checkbox" checked={on} onChange={(e) => setChosen(e.target.checked ? [...chosen, t.id] : chosen.filter((id) => id !== t.id))} />
                <TagChip tag={t} />
              </label>
            )
          })}
        </div>
      )}
      <div className="row">
        <button type="button" className="primary" disabled={scope === 'tags' && chosen.length === 0} onClick={() => onSave(scope, scope === 'tags' ? chosen : [])}>Save</button>
        <button type="button" className="linklike" onClick={onCancel}>Cancel</button>
      </div>
    </fieldset>
  )
}

/** Warns about monitors whose alerts no destination is routed to: they go to the team's own addresses, or, without any, to nobody. */
export function CoverageWarning({ coverage }: { coverage: AlertCoverage }) {
  const missing = coverage.uncovered_monitors
  if (missing.length === 0) return null
  const shown = missing.slice(0, 5).map((m) => m.name).join(', ') + (missing.length > 5 ? ` and ${missing.length - 5} more` : '')
  const which = missing.length === 1 ? '1 monitor is' : `${missing.length} monitors are`
  return coverage.fallback_available ? (
    <Alert tone="warning" role="status">
      {which} not routed to any destination: {shown}. Their alerts go to your team members’ own email addresses instead. Send a destination alerts for all
      monitors, or for their tags, to choose where they go.
    </Alert>
  ) : (
    <Alert>
      <strong>Nobody would be alerted</strong> if {missing.length === 1 ? 'this monitor goes' : 'these monitors go'} offline: {shown}. No destination is routed to{' '}
      {missing.length === 1 ? 'it' : 'them'}, and no team member has their own address here to fall back to. Send a destination alerts for all monitors, or
      for their tags.
    </Alert>
  )
}

function StatusLabel({ s }: { s: NotificationSetting }) {
  if (!s.verified) return <span className="d-badge d-badge-soft d-badge-warning"><span aria-hidden="true">⏳</span> Waiting for confirmation</span>
  if (!s.enabled) return <span className="d-badge d-badge-soft d-badge-ghost"><span aria-hidden="true">⏸</span> Alerts off</span>
  return <span className="d-badge d-badge-soft d-badge-success"><span aria-hidden="true">✔</span> Receiving alerts</span>
}

export function NotificationsPage() {
  const { user, organization } = useAuth()
  const isOwner = organization?.role === 'owner'
  const mine = (s: NotificationSetting) => s.user_id !== null && s.user_id === user?.id
  const verified = Boolean(user?.email_verified_at)

  const [items, setItems] = useState<NotificationSetting[] | null>(null)
  const [max, setMax] = useState(10)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [channel, setChannel] = useState<Channel>('email')
  const [address, setAddress] = useState('')
  const [botToken, setBotToken] = useState('') // Telegram, own bot only
  const [region, setRegion] = useState<'us' | 'eu'>('us') // PagerDuty: the account's service region
  const [telegramBot, setTelegramBot] = useState('') // HawkEye's bot, when chats can be connected through it
  const [ownBot, setOwnBot] = useState(false) // the advanced option: the customer's own bot
  const [secret, setSecret] = useState<string | null>(null)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState<string | null>(null) // the recipient a test is being sent to right now
  const [tags, setTags] = useState<TagSummary[]>([])
  const [coverage, setCoverage] = useState<AlertCoverage | null>(null)
  const [routing, setRouting] = useState<string | null>(null) // the destination whose routing is being edited

  const load = useCallback(async () => {
    try {
      const r = await api<{ notification_settings: NotificationSetting[]; max: number; telegram_bot?: string }>('GET', '/notification-settings')
      setItems(r.notification_settings)
      setMax(r.max)
      setTelegramBot(r.telegram_bot ?? '')
      setError(null)
      // Routing details only add to the list; it still works without them.
      const [t, c] = await Promise.all([
        api<{ tags?: TagSummary[] }>('GET', '/tags').catch(() => ({ tags: [] })),
        api<AlertCoverage>('GET', '/notification-settings/coverage').catch(() => null),
      ])
      setTags(t.tags ?? [])
      setCoverage(c && Array.isArray(c.uncovered_monitors) ? c : null)
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

  /** Sends a test alert to one recipient: a test email, or a test message to a chat app or webhook. */
  async function sendTest(s: NotificationSetting) {
    if (testing) return
    setTesting(s.id)
    const sent =
      s.channel === 'email'
        ? `A test alert was sent to ${s.destination}. It should arrive within a minute; if it doesn’t, check the spam folder.`
        : 'Test message sent.'
    const off = s.enabled ? '' : ' Alerts are turned off for this recipient, so real alerts will not be sent here until you turn them on.'
    await run(() => api('POST', `/notification-settings/${s.id}/test`), sent + off)
    setTesting(null)
  }

  async function add(e: SubmitEvent) {
    e.preventDefault()
    setBusy(true)
    setFieldErrors({})
    setError(null)
    setNotice(null)
    setSecret(null)
    try {
      const body = channel === 'telegram' ? { channel, destination: address, bot_token: botToken }
        : channel === 'pagerduty' ? { channel, destination: address, region }
          : { channel, destination: address }
      const r = await api<{ notification_setting: NotificationSetting; signing_secret?: string }>('POST', '/notification-settings', body)
      setAddress('')
      setBotToken('')
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
      if (err instanceof ApiError && (err.fields.destination || err.fields.bot_token || err.fields.region)) setFieldErrors(err.fields)
      else setError(err instanceof ApiError ? err.message : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  const full = (items?.length ?? 0) >= max
  const viaHawkEyeBot = channel === 'telegram' && telegramBot !== '' && !ownBot

  const telegramConnected = useCallback(
    (s: NotificationSetting) => {
      setNotice(`Connected. ${s.destination} will receive alerts in Telegram, and we sent it a confirmation message.`)
      void load()
    },
    [load],
  )

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
      <p>Everything listed here is told when a monitor is confirmed offline, when it recovers, and when a certificate or domain is about to expire: by email, in Slack, Discord, Microsoft Teams or Telegram, in PagerDuty, or by a signed webhook to your own systems.</p>
      <p className="hint">
        Each destination gets alerts for all monitors, or only for monitors with chosen tags, so each client can get just their own sites’ alerts. To delay a
        monitor’s alerts or hold them overnight, edit the monitor.
      </p>

      {!verified && (
        <Alert tone="warning" role="status">
          Verify your own email address first — until then you can’t add other recipients, and your own address won’t receive alerts.
        </Alert>
      )}
      {error && <Alert>{error}</Alert>}
      {notice && <Alert tone="success" role="status">{notice}</Alert>}
      {coverage && <CoverageWarning coverage={coverage} />}
      {secret && (
        <Alert tone="warning" role="status">
          Signing secret: <code>{secret}</code>. Copy it now — it is not shown again. HawkEye signs every request with it (HMAC-SHA256 in the <code>X-HawkEye-Signature</code> header), so your server can check a request is genuine.
        </Alert>
      )}
      {items === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {items && (
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Alert recipients</caption>
            <thead>
              <tr><th>Destination</th><th>Status</th><th>Alerts for</th>{(isOwner || items.some(mine)) && <th>Actions</th>}</tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <tr key={s.id}>
                  <td>
                    {s.channel !== 'email' && <span className="hint">{channelNames[s.channel]}: </span>}
                    {s.destination}
                    {mine(s) ? <span className="hint"> (you)</span> : s.user_id && <span className="hint"> (team member)</span>}
                  </td>
                  <td><StatusLabel s={s} /></td>
                  <td>
                    {routing === s.id ? (
                      <RoutingEditor s={s} tags={tags} onCancel={() => setRouting(null)}
                        onSave={(scope, tagIDs) => void run(async () => {
                          await api('PUT', `/notification-settings/${s.id}/routing`, { monitor_scope: scope, tag_ids: tagIDs })
                          setRouting(null)
                        }, scope === 'all' ? 'Saved. This destination gets alerts for all monitors.' : 'Saved. This destination gets alerts only for monitors with those tags.')} />
                    ) : (
                      <>
                        <RoutingSummary s={s} tags={tags} />
                        {isOwner && (
                          <div><button type="button" className="linklike" onClick={() => setRouting(s.id)} aria-label={`Change which monitors ${s.destination} gets alerts for`}>Change</button></div>
                        )}
                      </>
                    )}
                  </td>
                  {!isOwner && items.some(mine) && (
                    // A member may turn off or remove only their own address, added when they joined.
                    <td className="actions">
                      {mine(s) && (
                        <>
                          <button onClick={() => void run(() => api('PATCH', `/notification-settings/${s.id}`, { enabled: !s.enabled }))}>
                            {s.enabled ? 'Turn off' : 'Turn on'}
                          </button>
                          <button className="danger" onClick={() => remove(s)} aria-label={`Remove ${s.destination}`}>Remove</button>
                        </>
                      )}
                    </td>
                  )}
                  {isOwner && (
                    <td className="actions">
                      {s.verified && (
                        <span className="d-tooltip" data-tip={s.channel === 'email' ? 'Email a test alert to this address now' : 'Deliver a test message to this destination now'}>
                          <button type="button" disabled={testing !== null} onClick={() => void sendTest(s)}>
                            {testing === s.id ? 'Sending…' : 'Send test alert'}
                          </button>
                        </span>
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
              <select id="channel" value={channel} onChange={(e) => { setChannel(e.target.value as Channel); setFieldErrors({}) }}>
                {(Object.keys(channelNames) as Channel[]).map((c) => <option key={c} value={c}>{channelNames[c]}</option>)}
              </select>
            </div>
            {channel === 'email' ? (
              <Field label="Email address" type="email" required value={address} onChange={(e) => setAddress(e.target.value)}
                error={fieldErrors.destination} disabled={!verified || full}
                hint="New addresses must confirm via an emailed link before they receive alerts." />
            ) : viaHawkEyeBot ? (
              <>
                <TelegramConnect bot={telegramBot} disabled={!verified || full} items={items} onConnected={telegramConnected} onError={setError} />
                <p className="hint">
                  Advanced: <button type="button" className="linklike" onClick={() => setOwnBot(true)}>use your own bot instead</button>
                </p>
              </>
            ) : channel === 'telegram' ? (
              <>
                {telegramBot && (
                  <p className="hint">
                    <button type="button" className="linklike" onClick={() => { setOwnBot(false); setFieldErrors({}) }}>Use @{telegramBot} instead</button> (no token needed)
                  </p>
                )}
                <Field label="Bot token" required autoComplete="off" spellCheck={false} value={botToken} onChange={(e) => setBotToken(e.target.value)}
                  error={fieldErrors.bot_token} disabled={!verified || full} placeholder="123456789:AAE…"
                  hint="Message @BotFather in Telegram, send /newbot, and paste the token it gives you." />
                <Field label="Chat ID" required value={address} onChange={(e) => setAddress(e.target.value)}
                  error={fieldErrors.destination} disabled={!verified || full} placeholder="-1001234567890 or @yourchannel"
                  hint="Add the bot to your group (or as an admin of your channel). Use the group’s numeric ID, or @name for a public channel. We send a test message first." />
              </>
            ) : channel === 'pagerduty' ? (
              <>
                <Field label="Integration key" required autoComplete="off" spellCheck={false} value={address} onChange={(e) => setAddress(e.target.value)}
                  error={fieldErrors.destination} disabled={!verified || full} placeholder="32 characters, from your PagerDuty service"
                  hint="In PagerDuty, open the service → Integrations → Add an integration → Events API V2, and copy its Integration Key. We trigger a test incident and resolve it straight away." />
                <div className="field">
                  <label htmlFor="pd-region">PagerDuty account region</label>
                  <select id="pd-region" value={region} onChange={(e) => setRegion(e.target.value as 'us' | 'eu')} disabled={!verified || full}>
                    <option value="us">US (pagerduty.com)</option>
                    <option value="eu">EU (eu.pagerduty.com)</option>
                  </select>
                  {fieldErrors.region && <small className="error">{fieldErrors.region}</small>}
                  <small className="hint">An outage opens a PagerDuty incident and its recovery resolves it. Slowdowns do the same; certificate and domain warnings stay open until you resolve them.</small>
                </div>
              </>
            ) : (
              <Field label={urlFields[channel].label} type="url" required value={address} onChange={(e) => setAddress(e.target.value)}
                error={fieldErrors.destination} disabled={!verified || full} placeholder={urlFields[channel].placeholder}
                hint={urlFields[channel].hint} />
            )}
            {!viaHawkEyeBot && <button type="submit" disabled={busy || !verified || full}>{channel === 'email' ? 'Add recipient' : 'Add destination'}</button>}
            {items && items.length >= max && <p className="hint">You have reached the limit of {max} recipients.</p>}
          </form>
        </section>
      ) : (
        <p className="hint">Only the organization owner can add or change alert recipients. You can turn off or remove your own address.</p>
      )}
    </>
  )
}
