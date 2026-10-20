// Google Analytics is ON by default (the service is aimed at US visitors, where analytics is opt-out) and a visitor can turn it off at any time
// from the "Cookie settings" link. A visitor whose browser sends the Global Privacy Control signal is treated as opted out unless they choose otherwise.
// An explicit choice is kept in a first-party cookie (see the cookie table in the Privacy Policy). Page addresses can carry one-time tokens in the
// query string or fragment, so only the origin and path are ever sent: never location.href. The one exception is a short allow-list of campaign
// parameters (utm_* and ad click IDs), which Google Analytics needs to attribute a visit to an ad campaign.
// If the service starts taking customers in the EU/UK, analytics must go back to opt-in (consent before anything loads): see docs/security.md.
import { useSyncExternalStore } from 'react'

const MEASUREMENT_ID = 'G-ZNT8EMTFGY'
const CHOICE_COOKIE = 'hawkeye_analytics'
const CHOICE_MAX_AGE = 60 * 60 * 24 * 365

export type Choice = 'on' | 'off'

// The only query parameters that ever leave the browser. Never add anything that can carry a secret (token, email, reset or verification links).
const CAMPAIGN_PARAMS = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content', 'gclid', 'msclkid', 'fbclid']

/** The current address reduced to origin, path and the campaign allow-list: safe to send to Google. */
export function safeLocation(pathname: string): string {
  const current = new URLSearchParams(window.location.search)
  const kept = new URLSearchParams()
  for (const name of CAMPAIGN_PARAMS) {
    const value = current.get(name)
    if (value) kept.set(name, value.slice(0, 200))
  }
  const query = kept.toString()
  return window.location.origin + pathname + (query ? `?${query}` : '')
}

type Gtag = (...args: unknown[]) => void
type AnalyticsWindow = Window & { dataLayer?: unknown[]; gtag?: Gtag } & Record<string, unknown>
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

function loadAnalytics() {
  if (isLocal()) return // never count local development or test runs
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
}

// Google's cookies sit on the registrable domain, so try the host and each parent domain.
function deleteAnalyticsCookies() {
  const parts = window.location.hostname.split('.')
  const domains = ['']
  for (let i = 0; i <= parts.length - 2; i++) domains.push(`; Domain=${parts.slice(i).join('.')}`)
  for (const name of document.cookie.split('; ').map((c) => c.split('=')[0])) {
    if (name !== '_ga' && !name.startsWith('_ga_') && name !== '_gid' && !name.startsWith('_gat')) continue
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
    deleteAnalyticsCookies()
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
