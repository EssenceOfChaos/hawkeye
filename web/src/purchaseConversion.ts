// Reports a paid subscription to Google Ads once the billing provider has confirmed it, even if the buyer was not looking at the billing page at that moment.
// Returning from Checkout only marks the purchase as pending (the provider confirms by webhook, which can take longer than the buyer waits). The
// conversion is sent the first time we see the subscription is paid, on the billing page or on any signed-in page, and only once per period paid for.
import type { BillingSubscription } from './api'
import { trackPurchase } from './analytics'

const PENDING_KEY = 'hawkeye_purchase_pending'
const PENDING_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000 // a purchase confirmed a week later is no longer this ad click's conversion

function storage<T>(fn: (s: Storage) => T, fallback: T): T {
  try {
    return fn(window.localStorage)
  } catch {
    return fallback // storage unavailable: skip rather than risk counting a purchase twice
  }
}

/** Call when the buyer returns from a successful Checkout. */
export function markCheckoutReturn() {
  storage((s) => s.setItem(PENDING_KEY, String(Date.now())), undefined)
}

/** Whether a purchase is waiting for the provider's confirmation (so signed-in pages know to look). */
export function hasPendingPurchase(): boolean {
  return storage((s) => {
    const at = Number(s.getItem(PENDING_KEY))
    if (!at) return false
    if (Date.now() - at <= PENDING_MAX_AGE_MS) return true
    s.removeItem(PENDING_KEY)
    return false
  }, false)
}

/** Sends the conversion if a purchase is pending and the subscription is now paid. Safe to call on every refresh of the subscription. */
export function reportPurchaseIfPaid(sub: BillingSubscription) {
  if (!sub.paid_access || !sub.current_period_end || !hasPendingPurchase()) return
  const reported = `hawkeye_conversion_${sub.current_period_end}`
  const first = storage((s) => {
    if (s.getItem(reported)) return false
    s.setItem(reported, '1')
    s.removeItem(PENDING_KEY)
    return true
  }, false)
  if (first) trackPurchase(sub.plan.monthly_price_cents / 100, sub.current_period_end)
}
