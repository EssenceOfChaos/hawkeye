/**
 * The security log (internal/audit): what each event means, in words. The Go test TestEveryActionHasASentence keeps `sentences` in step with the
 * actions the server records (internal/audit/actions.go).
 */

export interface LogEvent {
  id: number
  occurred_at: string
  action: string
  actor: { type: 'user' | 'api_key' | 'oauth_app' | 'anonymous' | 'system'; email: string | null; credential_name: string | null }
  target: { type: string; id: string | null; name: string | null } | null
  details: Record<string, unknown>
  ip: string | null
  user_agent: string | null
  request_id: string | null
}

export interface LogPage {
  events: LogEvent[]
  next_before: number | null
  categories: string[]
  retention_days: number
}

/** Names for the categories the log can be filtered by (the part of an action before the dot). */
export const categoryNames: Record<string, string> = {
  account: 'Sign-in and account',
  organization: 'Organization',
  billing: 'Billing',
  member: 'Team',
  api_key: 'API keys',
  connected_app: 'Connected apps',
  alert_destination: 'Alert recipients',
  monitor: 'Monitors',
  status_page: 'Status pages',
  maintenance: 'Maintenance',
  incident_update: 'Incident updates',
  domain: 'Domains',
}

const quoted = (s: string | null | undefined) => (s ? `“${s}”` : '')
const name = (e: LogEvent) => quoted(e.target?.name) || '(name not recorded)'
const plain = (e: LogEvent) => e.target?.name ?? 'someone'
const access = (scope: unknown) => (scope === 'write' ? 'read and write' : 'read only')
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

function changedFields(e: LogEvent): string {
  const f = e.details.fields
  if (Array.isArray(f) && f.length > 0) return ` (${f.map((x) => String(x).replaceAll('_', ' ')).join(', ')})`
  const bulk = e.details.bulk
  if (typeof bulk === 'string') return ` (${bulk.replaceAll('_', ' ')}, for several monitors at once)`
  return ''
}

/** One sentence per action, about what was done (who did it is shown next to it). */
export const sentences: Record<string, (e: LogEvent) => string> = {
  'account.signed_up': () => 'Created the account',
  'account.signed_in': (e) =>
    e.details.method === 'recovery_code' ? 'Signed in with a recovery code' : e.details.method === 'authenticator' ? 'Signed in with an authenticator code' : 'Signed in',
  'account.sign_in_failed': (e) => (e.details.reason === 'code' ? 'Failed sign-in: wrong two-factor code' : 'Failed sign-in: wrong password'),
  'account.signed_out': () => 'Signed out',
  'account.password_changed': (e) => {
    const n = Number(e.details.other_sessions_ended ?? 0)
    return n > 0 ? `Changed the password and signed out ${plural(n, 'other session')}` : 'Changed the password'
  },
  'account.password_reset': (e) => {
    const n = Number(e.details.api_keys_revoked ?? 0)
    return `Reset the password with an emailed link${n > 0 ? ` (and revoked ${plural(n, 'API key')})` : ''}`
  },
  'account.email_verified': () => 'Confirmed the email address',
  'account.two_factor_enabled': () => 'Turned on two-factor sign-in',
  'account.two_factor_disabled': () => 'Turned off two-factor sign-in',
  'account.recovery_codes_regenerated': () => 'Made new recovery codes (the old ones stopped working)',
  'account.data_exported': () => 'Downloaded a copy of the account’s data',

  'organization.renamed': (e) => `Renamed the organization from ${quoted(String(e.details.from ?? ''))} to ${quoted(String(e.details.to ?? ''))}`,
  'organization.ownership_transferred': (e) => `Made ${plain(e)} the owner`,
  'organization.two_factor_requirement_changed': (e) =>
    e.details.required ? 'Required two-factor sign-in for everyone in the organization' : 'Stopped requiring two-factor sign-in',
  'organization.ai_drafts_changed': (e) => (e.details.enabled ? 'Turned on AI-drafted incident updates' : 'Turned off AI-drafted incident updates'),
  'billing.subscription_canceled': () => 'Canceled the subscription',
  'member.invited': (e) => `Invited ${plain(e)} to the team`,
  'member.invitation_resent': (e) => `Sent the invitation to ${plain(e)} again`,
  'member.invitation_revoked': (e) => `Canceled the invitation to ${plain(e)}`,
  'member.joined': () => 'Joined the team',
  'member.removed': (e) => `Removed ${plain(e)} from the team`,
  'member.left': (e) => (e.details.account_deleted ? 'Left the team by deleting their account' : 'Left the team'),
  'api_key.created': (e) => {
    const exp = typeof e.details.expires_at === 'string' ? `, expires ${new Date(e.details.expires_at).toLocaleDateString([], { dateStyle: 'medium' })}` : ', never expires'
    return `Created API key ${name(e)} (${access(e.details.scope)}${exp})`
  },
  'api_key.revoked': (e) => `Revoked API key ${name(e)}`,
  'connected_app.connected': (e) => `Connected ${plain(e)} (${access(e.details.scope)})`,
  'connected_app.disconnected': (e) => `Disconnected ${plain(e)}`,
  'alert_destination.added': (e) =>
    e.details.via === 'hawkeye_bot' ? `Connected the Telegram chat ${name(e)} for alerts` : `Added the alert recipient ${name(e)}`,
  'alert_destination.changed': (e) => {
    if (e.details.enabled === true) return `Turned on alerts to ${name(e)}`
    if (e.details.enabled === false) return `Turned off alerts to ${name(e)}`
    return `Changed which monitors send alerts to ${name(e)}`
  },
  'alert_destination.removed': (e) =>
    e.details.via === 'hawkeye_bot' ? `The Telegram chat ${name(e)} stopped receiving alerts (from Telegram)` : `Removed the alert recipient ${name(e)}`,
  'monitor.created': (e) => `Created the monitor ${name(e)}`,
  'monitor.changed': (e) => `Changed the monitor ${name(e)}${changedFields(e)}`,
  'monitor.deleted': (e) => `Deleted the monitor ${name(e)}`,
  'monitor.paused': (e) => `Paused the monitor ${name(e)}`,
  'monitor.resumed': (e) => `Resumed the monitor ${name(e)}`,
  'monitor.badge_enabled': (e) => `Turned on the public uptime badge for ${name(e)}`,
  'monitor.badge_disabled': (e) => `Turned off the public uptime badge for ${name(e)}`,
  'monitor.ping_address_rotated': (e) => `Made a new ping address for ${name(e)} (the old one stopped working)`,
  'status_page.created': (e) => `Created the status page ${name(e)}`,
  'status_page.changed': (e) =>
    e.details.logo === 'uploaded' ? `Uploaded a logo for the status page ${name(e)}`
      : e.details.logo === 'removed' ? `Removed the logo from the status page ${name(e)}`
        : `Changed the status page ${name(e)}${e.details.published === true ? ' (published)' : e.details.published === false ? ' (not published)' : ''}`,
  'status_page.deleted': (e) => `Deleted the status page ${name(e)}`,
  'maintenance.scheduled': (e) => `Scheduled the maintenance window ${name(e)}`,
  'maintenance.deleted': (e) => `Deleted the maintenance window ${name(e)}`,
  'incident_update.posted': (e) => `Posted a public incident update (${String(e.details.status ?? 'update')})${e.details.message ? `: ${quoted(String(e.details.message))}` : ''}`,
  'incident_update.deleted': () => 'Deleted a public incident update',
  'domain.added': (e) => `Added the domain ${plain(e)}`,
  'domain.removed': (e) => `Removed the domain ${plain(e)}`,
}

export function describe(e: LogEvent): string {
  return sentences[e.action]?.(e) ?? e.action
}

/** Who did it: a person, a person's API key, a connected app acting for a person, someone not signed in, or HawkEye itself. */
export function who(e: LogEvent): { label: string; note: string | null } {
  const a = e.actor
  switch (a.type) {
    case 'api_key':
      return { label: a.email ?? 'An API key', note: `with the API key ${quoted(a.credential_name) || '(name not recorded)'}` }
    case 'oauth_app':
      return { label: a.email ?? 'A connected app', note: `through ${a.credential_name ?? 'a connected app'}` }
    case 'anonymous':
      return { label: 'Someone not signed in', note: null }
    case 'system':
      return { label: e.details.via === 'hawkeye_bot' ? 'Telegram' : 'HawkEye', note: null }
    default:
      return { label: a.email ?? 'A former member', note: null }
  }
}

/** A short name for the browser or tool a request came from, read from its User-Agent. */
export function device(ua: string | null): string | null {
  if (!ua) return null
  const browser = /Edg\//.test(ua) ? 'Edge' : /OPR\//.test(ua) ? 'Opera' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : null
  const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : null
  if (browser) return os ? `${browser} on ${os}` : browser
  return ua.split(/\s/)[0].slice(0, 40) // a tool: curl/8.5.0, python-requests/2.32, Claude-User...
}
