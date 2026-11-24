import { api, ApiError } from './api'
import { analyticsEnabled, trackEvent, trackRegistration } from './analytics'
import { attributionForSignup, forgetArrival } from './attribution'

/**
 * Sign in with Google, GitHub and X (docs/plan-social-sign-in.md), next to email and password. The server does the work: `start` answers with the
 * provider's address, and the provider sends the browser back to the API, which redirects into the app with what happened in the fragment.
 */

export type Provider = 'google' | 'github' | 'x'
export type SignInMethod = Provider | 'password'

export const PROVIDER_NAMES: Record<Provider, string> = { google: 'Google', github: 'GitHub', x: 'X' }

/** The order the buttons appear in. */
export const PROVIDER_ORDER: Provider[] = ['google', 'github', 'x']

/** The providers this server offers (none until their client IDs are set up). */
export async function availableProviders(): Promise<Provider[]> {
  try {
    const r = await api<{ providers: string[] }>('GET', '/auth/social/providers')
    return PROVIDER_ORDER.filter((p) => r.providers.includes(p))
  } catch {
    return []
  }
}

/**
 * Sends the browser to the provider. `intent` is signin (sign in or create an account), connect (add to the signed-in account) or confirm (prove
 * it's you before a sensitive action, for an account without a password). `next` is where to come back to.
 */
export async function startSocial(provider: Provider, intent: 'signin' | 'connect' | 'confirm', next?: string) {
  const body: Record<string, unknown> = { intent, next }
  // How this browser first found HawkEye, for an account created on the way (nothing while analytics is off).
  if (intent === 'signin') body.attribution = attributionForSignup(analyticsEnabled())
  const r = await api<{ url: string }>('POST', `/auth/social/${provider}/start`, body)
  window.location.assign(r.url)
}

const isProvider = (v: string | null): v is Provider => v !== null && (PROVIDER_ORDER as string[]).includes(v)

const LAST_USED_KEY = 'hawkeye_last_sign_in'

/**
 * How this browser last signed in, for the "Last used" badge. Only the method is kept (never the email), in this browser only; it is a
 * preference, so it isn't tied to the analytics setting. Storage may be unavailable (private windows): then there is simply no badge.
 */
export function lastUsed(): SignInMethod | null {
  try {
    const v = localStorage.getItem(LAST_USED_KEY)
    return v === 'password' || isProvider(v) ? v : null
  } catch {
    return null
  }
}

export function rememberSignIn(method: SignInMethod) {
  try {
    localStorage.setItem(LAST_USED_KEY, method)
  } catch {
    // no storage: no badge
  }
}

/** What the API's redirect after a provider put in the fragment. */
export interface SocialReturn {
  signedIn?: Provider
  signedUpEventId?: string
  confirmed?: Provider
  /** Two-factor accounts: the code step to finish, which provider came first, and where to go afterward. */
  challenge?: string
  via?: Provider
  next?: string
}

const asProvider = (v: string | null): Provider | undefined => (isProvider(v) ? v : undefined)

export function readSocialReturn(hash: string): SocialReturn {
  const q = new URLSearchParams(hash.replace(/^#/, ''))
  return {
    signedIn: asProvider(q.get('signed-in')),
    signedUpEventId: q.get('signed-up') ?? undefined,
    confirmed: asProvider(q.get('confirmed')),
    challenge: q.get('challenge') ?? undefined,
    via: asProvider(q.get('via')),
    next: q.get('next') ?? undefined,
  }
}

/**
 * Called once when the app loads: after a provider sign-in, remember the method for "Last used", count a new account as a sign-up (as the form
 * does), and take the fragment out of the address bar. The two-factor challenge is left for the sign-in page, which reads it itself.
 */
export function finishSocialReturn() {
  const r = readSocialReturn(window.location.hash)
  if (!r.signedIn) return
  rememberSignIn(r.signedIn)
  if (r.signedUpEventId) {
    forgetArrival()
    trackEvent('sign_up', { method: r.signedIn })
    trackRegistration(r.signedUpEventId)
  }
  window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search)
}

/** Sentences for the error codes the API's redirect puts in ?error= (sign-in) and ?social_error= (settings). */
export const SOCIAL_ERRORS: Record<string, string> = {
  canceled: 'Sign-in was canceled. Choose a way to sign in to try again.',
  expired: 'That sign-in took too long or was started in another browser. Please try again.',
  email_not_verified: 'Your Google, GitHub or X account doesn’t have a verified email address. Verify one there, or sign in with email and password.',
  disabled: 'This account has been disabled. Contact support if you think that’s a mistake.',
  identity_in_use: 'That Google, GitHub or X account is already connected to a different HawkEye account.',
  provider_connected: 'Your account already has a different account from this provider connected. Disconnect it first.',
  wrong_account: 'That isn’t the account connected to your HawkEye account. Use the one listed under Sign-in methods.',
  failed: 'We couldn’t sign you in that way. Please try again, or sign in with email and password.',
}

export const socialError = (code: string | null) => (code ? (SOCIAL_ERRORS[code] ?? SOCIAL_ERRORS.failed) : null)

/**
 * What the account page knows for confirming it's you before a sensitive action: with a password, the password; without one, a recent
 * confirmation through a connected provider (`confirmed`, set when the provider sent the browser back here).
 */
export interface Reauth {
  hasPassword: boolean
  /** The providers connected to this account, which can confirm it's you. */
  connected: Provider[]
  confirmed: Provider | null
  /** Forget a confirmation the server no longer accepts (it lasts 10 minutes). */
  expire: () => void
}

/** True when an error means the confirmation through a provider is missing or has run out. */
export const needsConfirmation = (e: unknown) => e instanceof ApiError && e.code === 'CONFIRMATION_REQUIRED'

/** Whether the password field, or the confirmation through a provider, has what the action needs. */
export const reauthReady = (reauth: Reauth, password: string) => (reauth.hasPassword ? password !== '' : reauth.confirmed !== null)

