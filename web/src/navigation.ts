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
