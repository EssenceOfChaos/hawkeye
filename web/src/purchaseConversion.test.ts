import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BillingSubscription } from './api'

vi.mock('./analytics', () => ({ trackPurchase: vi.fn() }))
import { trackPurchase } from './analytics'
import { hasPendingPurchase, markCheckoutReturn, reportPurchaseIfPaid } from './purchaseConversion'

const pro = { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90 }
const sub = (o: Partial<BillingSubscription> = {}): BillingSubscription => ({
  billing_enabled: true, plan: pro, status: 'active', paid_access: true, cancel_at_period_end: false,
  current_period_end: '2026-11-30T00:00:00Z', can_manage: true, has_subscription: true, ...o,
})

beforeEach(() => {
  localStorage.clear()
  vi.mocked(trackPurchase).mockClear()
})
afterEach(() => vi.useRealTimers())

describe('purchase conversion', () => {
  it('reports a confirmed purchase once, with the plan price and the period end as the transaction id', () => {
    markCheckoutReturn()
    reportPurchaseIfPaid(sub())
    reportPurchaseIfPaid(sub())
    expect(trackPurchase).toHaveBeenCalledTimes(1)
    expect(trackPurchase).toHaveBeenCalledWith(19, '2026-11-30T00:00:00Z')
    expect(hasPendingPurchase()).toBe(false)
  })

  it('waits for the provider: nothing is sent while the subscription is unpaid, and it is sent when it later becomes paid', () => {
    markCheckoutReturn()
    reportPurchaseIfPaid(sub({ paid_access: false, status: 'incomplete' }))
    expect(trackPurchase).not.toHaveBeenCalled()
    expect(hasPendingPurchase()).toBe(true) // still waiting: a later visit to any page can report it
    reportPurchaseIfPaid(sub())
    expect(trackPurchase).toHaveBeenCalledTimes(1)
  })

  it('never reports a subscription that did not just come back from Checkout (renewals, old customers)', () => {
    reportPurchaseIfPaid(sub())
    expect(trackPurchase).not.toHaveBeenCalled()
  })

  it('drops a pending purchase that is a week old', () => {
    vi.useFakeTimers()
    markCheckoutReturn()
    vi.advanceTimersByTime(8 * 24 * 60 * 60 * 1000)
    expect(hasPendingPurchase()).toBe(false)
    reportPurchaseIfPaid(sub())
    expect(trackPurchase).not.toHaveBeenCalled()
  })

  it('does not report the same paid period twice, even after another Checkout return', () => {
    markCheckoutReturn()
    reportPurchaseIfPaid(sub())
    markCheckoutReturn()
    reportPurchaseIfPaid(sub())
    expect(trackPurchase).toHaveBeenCalledTimes(1)
  })

  it('sends nothing when storage is unavailable rather than risk a double count', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    markCheckoutReturn()
    reportPurchaseIfPaid(sub())
    expect(trackPurchase).not.toHaveBeenCalled()
    vi.restoreAllMocks()
  })
})
