// Google Analytics is ON by default (the service is aimed at US visitors, where analytics is opt-out) and a visitor can turn it off at any time
// from the "Cookie settings" link. A visitor whose browser sends the Global Privacy Control signal is treated as opted out unless they choose otherwise.
// An explicit choice is kept in a first-party cookie (see the cookie table in the Privacy Policy). Page addresses can carry one-time tokens in the
// query string or fragment, so only the origin and path are ever sent: never location.href. The one exception is a short allow-list of campaign
// parameters (utm_* and ad click IDs), which Google Analytics needs to attribute a visit to an ad campaign.
// If the service starts taking customers in the EU/UK, analytics must go back to opt-in (consent before anything loads): see docs/security.md.
//
// The OpenAI Ads measurement pixel (conversions from ads in ChatGPT) follows the same choice. Unlike Google's tag, its SDK reads the page address itself
// and cannot be handed a cleaned one, so it is only loaded, and only sent events, while the address carries nothing beyond a short allow-list
// (addressIsShareable): a one-time token in a reset, verification or invitation link can never reach it.
import { useSyncExternalStore } from 'react'
import { campaignFrom, forgetArrival, label } from './attribution'

const MEASUREMENT_ID = 'G-ZNT8EMTFGY'
// Google Ads conversion tracking: the account id, and the "Purchase" conversion action's send_to (account id / label).
const ADS_ID = 'AW-18527685454'
const ADS_PURCHASE_SEND_TO = `${ADS_ID}/A0aqCPXL2aMdEM6W2IJF`
const CHOICE_COOKIE = 'hawkeye_analytics'
// OpenAI Ads: the data source's pixel id (from Ads Manager → Conversions) and its SDK.
const OPENAI_PIXEL_ID = 'BQzFQqkTH5HXxdU4vT171u'
const OPENAI_SDK_URL = 'https://bzrcdn.openai.com/sdk/oaiq.min.js'
// Setting this localStorage key to "1" turns on the SDK's console logging in your own browser, to check events while testing.
const OPENAI_DEBUG_KEY = 'hawkeye_oaiq_debug'
// The pixel's own cookies (attribution and browser reference), removed when analytics is turned off.
const OPENAI_COOKIES = ['__oppref', '__obref']
const CHOICE_MAX_AGE = 60 * 60 * 24 * 365

export type Choice = 'on' | 'off'

// The only query parameters that ever leave the browser. Never add anything that can carry a secret (token, email, reset or verification links).
const CAMPAIGN_PARAMS = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'gclid', 'msclkid', 'fbclid']

// Query parameters the OpenAI pixel may see besides the campaign ones: its own ad click ID, and app parameters that never carry anything personal
// (/billing?checkout=success after paying, /billing?interval=year from a pricing card).
// ref is the tag directories such as Product Hunt add to their links (?ref=producthunt), allowed while it is a short plain label.
const PIXEL_PARAMS = ['oppref', 'checkout', 'interval', 'ref']

/** Whether the OpenAI pixel may run on the current address: no fragment, and no query parameter outside the allow-lists. */
export function addressIsShareable(): boolean {
  if (window.location.hash) return false
  const q = new URLSearchParams(window.location.search)
  for (const name of q.keys()) {
    if (!CAMPAIGN_PARAMS.includes(name) && !PIXEL_PARAMS.includes(name)) return false
  }
  if (q.has('ref') && !label(q.get('ref'))) return false
  return true
}

/**
 * The current address reduced to origin, path and the campaign allow-list: safe to send to Google. A ?ref= tag (from a directory such as Product
 * Hunt) is passed as utm_source with utm_medium=referral when the link has no utm_source, since Google Analytics reads only utm_* tags.
 */
export function safeLocation(pathname: string): string {
  const current = new URLSearchParams(window.location.search)
  const kept = new URLSearchParams()
  for (const name of CAMPAIGN_PARAMS) {
    const value = current.get(name)
    if (value) kept.set(name, value.slice(0, 200))
  }
  const ref = label(current.get('ref'))
  if (ref && !kept.has('utm_source')) {
    kept.set('utm_source', campaignFrom(`?ref=${encodeURIComponent(ref)}`).source!)
    if (!kept.has('utm_medium')) kept.set('utm_medium', 'referral')
  }
  const query = kept.toString()
  return window.location.origin + pathname + (query ? `?${query}` : '')
}

type Gtag = (...args: unknown[]) => void
type Oaiq = ((...args: unknown[]) => void) & { q?: unknown[] }
type AnalyticsWindow = Window & { dataLayer?: unknown[]; gtag?: Gtag; oaiq?: Oaiq } & Record<string, unknown>
const w = () => window as unknown as AnalyticsWindow

let editing = false
const listeners = new Set<() => void>()
function emit() {
  listeners.forEach((l) => l())
}

/** The visitor's explicit choice, or null if they have not made one. */
export function readChoice(): Choice | null {
  const match = document.cookie.match(new RegExp(`(?:^|; )${CHOICE_COOKIE}=(on|off)(?:;|$)`))
  return match ? (match[1] as Choice) : null
}

/** Whether analytics runs: the explicit choice wins, then the browser's Global Privacy Control signal, then the default (on). */
export function analyticsEnabled(): boolean {
  const choice = readChoice()
  if (choice) return choice === 'on'
  return !(navigator as Navigator & { globalPrivacyControl?: boolean }).globalPrivacyControl
}

const isLocal = () => ['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)

function openAIDebug(): boolean {
  try {
    return window.localStorage.getItem(OPENAI_DEBUG_KEY) === '1'
  } catch {
    return false
  }
}

/** Loads the OpenAI pixel (OpenAI's setup snippet, run from here because the CSP forbids inline scripts), or re-grants consent if it is loaded. */
function loadOpenAIPixel() {
  if (isLocal()) return
  const win = w()
  if (win.oaiq) {
    win.oaiq('consent', true)
    return
  }
  if (!addressIsShareable()) return // loaded at the next page whose address is safe
  const oaiq: Oaiq = function oaiq() {
    // The SDK reads the queued calls as `arguments` objects, as in OpenAI's snippet.
    // eslint-disable-next-line prefer-rest-params
    oaiq.q!.push(arguments)
  }
  oaiq.q = []
  win.oaiq = oaiq
  const script = document.createElement('script')
  script.async = true
  script.src = OPENAI_SDK_URL
  document.head.appendChild(script)
  oaiq('init', { pixelId: OPENAI_PIXEL_ID, debug: openAIDebug() })
}

/** Sends one event to the OpenAI pixel, loading it first if needed, while analytics is on and the address is safe to expose. */
function measure(event: string, data: Record<string, unknown>, options?: Record<string, unknown>) {
  if (!analyticsEnabled() || isLocal() || !addressIsShareable()) return
  if (!w().oaiq) loadOpenAIPixel()
  const oaiq = w().oaiq
  if (typeof oaiq !== 'function') return
  if (options) oaiq('measure', event, data, options)
  else oaiq('measure', event, data)
}

function loadAnalytics() {
  if (isLocal()) return // never count local development or test runs
  loadOpenAIPixel()
  const win = w()
  win[`ga-disable-${MEASUREMENT_ID}`] = false
  if (win.gtag) return // already loaded this visit
  win.dataLayer = win.dataLayer || []
  // gtag.js needs the real `arguments` object in the queue, not an array of them.
  win.gtag = function gtag() {
    // eslint-disable-next-line prefer-rest-params
    win.dataLayer!.push(arguments)
  }
  const script = document.createElement('script')
  script.async = true
  script.src = `https://www.googletagmanager.com/gtag/js?id=${MEASUREMENT_ID}`
  document.head.appendChild(script)
  win.gtag('js', new Date())
  win.gtag('config', MEASUREMENT_ID, { send_page_view: false, allow_google_signals: false, allow_ad_personalization_signals: false })
  win.gtag('config', ADS_ID, { allow_ad_personalization_signals: false })
}

// Google's cookies sit on the registrable domain, so try the host and each parent domain.
function deleteAnalyticsCookies() {
  const parts = window.location.hostname.split('.')
  const domains = ['']
  for (let i = 0; i <= parts.length - 2; i++) domains.push(`; Domain=${parts.slice(i).join('.')}`)
  for (const name of document.cookie.split('; ').map((c) => c.split('=')[0])) {
    if (name !== '_ga' && !name.startsWith('_ga_') && name !== '_gid' && !name.startsWith('_gat') && !OPENAI_COOKIES.includes(name)) continue
    for (const d of domains) document.cookie = `${name}=; Max-Age=0; Path=/${d}`
  }
}

export function setChoice(choice: Choice) {
  const secure = window.location.protocol === 'https:' ? '; Secure' : ''
  document.cookie = `${CHOICE_COOKIE}=${choice}; Max-Age=${CHOICE_MAX_AGE}; Path=/; SameSite=Lax${secure}`
  editing = false
  if (choice === 'on') {
    const wasLoaded = !!w().gtag
    loadAnalytics()
    if (!wasLoaded) trackPageView(window.location.pathname)
  } else {
    w()[`ga-disable-${MEASUREMENT_ID}`] = true
    w().oaiq?.('consent', false) // stops the pixel's pings and removes its cookies (deleted below too, in case it is not loaded this visit)
    deleteAnalyticsCookies()
    forgetArrival() // how this browser found HawkEye (attribution.ts) is analytics too
  }
  emit()
}

/** Opens the cookie settings panel (the "Cookie settings" footer links). */
export function openCookieSettings() {
  editing = true
  emit()
}

/** Call once at startup: loads analytics unless the visitor has opted out. */
export function initAnalytics() {
  if (analyticsEnabled()) loadAnalytics()
}

export function trackPageView(pathname: string) {
  measure('page_viewed', { type: 'contents' })
  const gtag = w().gtag
  if (!analyticsEnabled() || typeof gtag !== 'function') return
  gtag('event', 'page_view', {
    page_location: safeLocation(pathname),
    page_path: pathname,
    page_title: document.title,
  })
}

/** A named conversion event (for example sign_up). Parameters must never contain personal data. */
export function trackEvent(name: string, params: Record<string, string> = {}) {
  const gtag = w().gtag
  if (!analyticsEnabled() || typeof gtag !== 'function') return
  gtag('event', name, params)
}

/** A confirmed paid subscription, reported to Google Ads. Call once per purchase; `valueUsd` is the monthly price and `transactionId` lets Google drop duplicates. */
export function trackPurchase(valueUsd: number, transactionId: string) {
  const gtag = w().gtag
  if (!analyticsEnabled() || typeof gtag !== 'function') return
  gtag('event', 'conversion', { send_to: ADS_PURCHASE_SEND_TO, value: valueUsd, currency: 'USD', transaction_id: transactionId })
}

/** A completed registration, for OpenAI Ads (Google Analytics gets the sign_up event through trackEvent). */
export function trackRegistration() {
  measure('registration_completed', { type: 'customer_action' })
}

/**
 * A confirmed paid subscription, for OpenAI Ads: what was paid, in cents (a year's price for yearly billing), and the plan and interval. `eventId` is the
 * same value Google gets as the transaction id, so a repeat is dropped.
 */
export function trackSubscription(cents: number, planCode: string, interval: 'month' | 'year', eventId: string) {
  measure('subscription_created', { type: 'plan_enrollment', plan_id: `${planCode}_${interval}`, amount: cents, currency: 'USD' }, { event_id: eventId })
}

/** What the settings panel needs: whether to show it, and whether analytics is currently on. */
export function useCookieSettings() {
  const subscribe = (cb: () => void) => {
    listeners.add(cb)
    return () => void listeners.delete(cb)
  }
  const isEditing = useSyncExternalStore(subscribe, () => editing, () => false)
  const choice = useSyncExternalStore(subscribe, readChoice, () => null)
  return { visible: isEditing, enabled: choice ? choice === 'on' : analyticsEnabled() }
}
