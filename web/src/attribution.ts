// How a visitor found HawkEye, remembered in this browser until they sign up, and then sent with the sign-up so it is recorded with the account
// (internal/attribution). Only short labels are kept: the campaign tags (utm_source, utm_medium, utm_campaign, or ?ref=), which kind of ad click
// brought them (never the click ID itself), the host of the site that linked here, and the path of the page they landed on (never its query string,
// which can carry one-time tokens). It follows the analytics choice: nothing is remembered or sent while analytics is off, and turning analytics off
// forgets it. It is kept for 90 days and forgotten after signing up.
//
// The first visit that carries a campaign tag or ad click wins; until one does, the first visit is kept (for example "linked from news.ycombinator.com").

const KEY = 'hawkeye_first_touch'
const KEEP_MS = 90 * 24 * 60 * 60 * 1000

export type Attribution = {
  source?: string
  medium?: string
  campaign?: string
  click_id?: 'google_ads' | 'microsoft_ads' | 'meta_ads' | 'openai_ads'
  referrer_host?: string
  landing_path?: string
  first_seen_at: string
}

// The same rule the server applies: short labels only, so an email address, token or full address is never kept.
const LABEL = /^[A-Za-z0-9 ._+:/()-]{1,100}$/

/** The value if it is a short plain label, else undefined. */
export function label(value: string | null | undefined): string | undefined {
  const v = value?.trim()
  return v && LABEL.test(v) ? v : undefined
}

const CLICK_IDS: [string, NonNullable<Attribution['click_id']>, string][] = [
  ['gclid', 'google_ads', 'google'],
  ['msclkid', 'microsoft_ads', 'bing'],
  ['fbclid', 'meta_ads', 'facebook'],
  ['oppref', 'openai_ads', 'chatgpt'],
]

/** The campaign carried by a query string: utm_* first, then ?ref=, then the kind of ad click (as Google Analytics would name its source). */
export function campaignFrom(search: string): Pick<Attribution, 'source' | 'medium' | 'campaign' | 'click_id'> {
  const q = new URLSearchParams(search)
  const out: Pick<Attribution, 'source' | 'medium' | 'campaign' | 'click_id'> = {}
  const click = CLICK_IDS.find(([param]) => q.get(param))
  if (click) out.click_id = click[1]
  const utmSource = label(q.get('utm_source'))?.toLowerCase()
  const ref = label(q.get('ref'))?.toLowerCase()
  out.source = utmSource ?? ref ?? click?.[2]
  out.medium = label(q.get('utm_medium'))?.toLowerCase() ?? (!utmSource && ref ? 'referral' : click ? 'cpc' : undefined)
  out.campaign = label(q.get('utm_campaign'))
  for (const k of Object.keys(out) as (keyof typeof out)[]) if (out[k] === undefined) delete out[k]
  return out
}

function read(): Attribution | null {
  try {
    const raw = window.localStorage.getItem(KEY)
    if (!raw) return null
    const a = JSON.parse(raw) as Attribution
    if (!a || typeof a.first_seen_at !== 'string' || Date.now() - Date.parse(a.first_seen_at) > KEEP_MS) {
      window.localStorage.removeItem(KEY)
      return null
    }
    return a
  } catch {
    return null
  }
}

/** Call once per page load, with whether analytics is on: remembers how this visit arrived, unless an earlier visit already says more. */
export function rememberArrival(enabled: boolean) {
  if (!enabled) return
  const campaign = campaignFrom(window.location.search)
  const hasCampaign = Boolean(campaign.source || campaign.click_id)
  const stored = read()
  if (stored && (stored.source || stored.click_id || !hasCampaign)) return
  let referrer: string | undefined
  try {
    const host = document.referrer ? new URL(document.referrer).hostname.toLowerCase() : ''
    if (host && host !== window.location.hostname) referrer = host
  } catch {
    // an unreadable referrer is simply not recorded
  }
  const path = window.location.pathname
  const a: Attribution = {
    ...campaign,
    ...(referrer ? { referrer_host: referrer } : {}),
    ...(/^\/[A-Za-z0-9/._-]{0,199}$/.test(path) ? { landing_path: path } : {}),
    first_seen_at: new Date().toISOString(),
  }
  try {
    window.localStorage.setItem(KEY, JSON.stringify(a))
  } catch {
    // storage unavailable (private window, blocked site data): nothing is remembered
  }
}

/** What to send with a sign-up: what this browser remembered, or undefined while analytics is off. */
export function attributionForSignup(enabled: boolean): Attribution | undefined {
  return enabled ? read() ?? undefined : undefined
}

/** Forgets what was remembered: after signing up, and when analytics is turned off. */
export function forgetArrival() {
  try {
    window.localStorage.removeItem(KEY)
  } catch {
    // nothing to forget
  }
}
