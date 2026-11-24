import { useEffect, useState, type ComponentType } from 'react'
import { ApiError } from '../api'
import { availableProviders, lastUsed, PROVIDER_NAMES, startSocial, type Provider } from '../socialSignIn'
import { Alert } from './Alert'

/** Google's "G", as its sign-in branding guidelines require it (unchanged colors and shape). */
export function GoogleLogo() {
  return (
    <svg viewBox="0 0 48 48" width="18" height="18" aria-hidden="true" focusable="false">
      <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
      <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
      <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
      <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
    </svg>
  )
}

/** GitHub's mark (Octicons, MIT). */
export function GitHubLogo() {
  return (
    <svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true" focusable="false">
      <path fill="currentColor" d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  )
}

/** X's logo, in one color as its brand guidelines ask. */
export function XLogo() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" focusable="false">
      <path fill="currentColor" d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z" />
    </svg>
  )
}

const LOGOS: Record<Provider, ComponentType> = { google: GoogleLogo, github: GitHubLogo, x: XLogo }

export const ProviderLogo = ({ provider }: { provider: Provider }) => {
  const Logo = LOGOS[provider]
  return <Logo />
}

/** The "Last used" badge, on the way this browser signed in last time. */
export function LastUsedBadge() {
  return <span className="d-badge d-badge-sm d-badge-soft d-badge-info last-used">Last used</span>
}

/**
 * "Continue with Google", "Continue with GitHub" and "Continue with X", for the sign-in and sign-up pages. "Continue" fits both: a new address gets an account, a known
 * one signs in. Nothing shows until the server says which providers it offers, and nothing at all when it offers none. `onShown` says whether any
 * button is shown, so the page can draw the "or" between them and the email form.
 */
export function SocialButtons({ next, onShown }: { next?: string; onShown?: (shown: boolean) => void }) {
  const [providers, setProviders] = useState<Provider[]>([])
  const [busy, setBusy] = useState<Provider | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [last] = useState(lastUsed)

  useEffect(() => {
    let live = true
    void availableProviders().then((p) => {
      if (!live) return
      setProviders(p)
      onShown?.(p.length > 0)
    })
    return () => {
      live = false
    }
  }, [onShown])

  async function go(p: Provider) {
    setBusy(p)
    setError(null)
    try {
      await startSocial(p, 'signin', next)
      // The browser is leaving for the provider; keep the button busy until it does.
    } catch (e) {
      setBusy(null)
      setError(e instanceof ApiError ? e.message : 'Could not reach the server. Try again.')
    }
  }

  if (providers.length === 0) return null
  return (
    <div className="social-buttons">
      {providers.map((p) => (
        <button key={p} type="button" className={`social-btn social-btn-${p}`} disabled={busy !== null} onClick={() => void go(p)}>
          <ProviderLogo provider={p} />
          <span>{busy === p ? 'Opening…' : `Continue with ${PROVIDER_NAMES[p]}`}</span>
          {last === p && <> <LastUsedBadge /></>}
        </button>
      ))}
      {error && <Alert>{error}</Alert>}
    </div>
  )
}
