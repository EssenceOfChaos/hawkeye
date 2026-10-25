/**
 * Sends the browser to a payment-provider page (Checkout or the billing portal). Only https URLs are followed, so a bad
 * response can never turn into a javascript: or data: navigation.
 */
export function redirectTo(url: string): void {
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    throw new Error('The payment provider returned an invalid address.')
  }
  if (parsed.protocol !== 'https:') throw new Error('The payment provider returned an unsafe address.')
  window.location.assign(parsed.toString())
}

/**
 * Where to go once signed in: the page the visitor was sent away from (RequireAuth remembers it), or the dashboard. Only an address inside this app is
 * followed, so the remembered value can never send anyone to another site.
 */
export function afterLoginPath(state: unknown): string {
  const from = (state as { from?: unknown } | null)?.from
  if (typeof from !== 'string' || !from.startsWith('/') || from.startsWith('//') || from.startsWith('/\\') || from === '/login') return '/dashboard'
  return from
}
