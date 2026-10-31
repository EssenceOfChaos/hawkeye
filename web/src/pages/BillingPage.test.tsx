import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { BillingPage } from './BillingPage'

vi.mock('../navigation', () => ({ redirectTo: vi.fn() }))
import { redirectTo } from '../navigation'
vi.mock('../analytics', async (orig) => ({ ...(await orig<typeof import('../analytics')>()), trackPurchase: vi.fn() }))
import { trackPurchase } from '../analytics'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
  vi.mocked(redirectTo).mockClear()
  vi.mocked(trackPurchase).mockClear()
  localStorage.clear()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const free = { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7 }
const pro = { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90 }
const agency = { code: 'agency', name: 'Agency', monthly_price_cents: 5900, max_monitors: 200, minimum_check_interval_seconds: 60, history_retention_days: 365, max_team_members: 5 }

function sub(o: object = {}) {
  return { billing_enabled: true, plan: free, status: 'active', paid_access: false, cancel_at_period_end: false, current_period_end: null, can_manage: false, has_subscription: false, ...o }
}
const usage = (plan = free, used = 2) => ({ plan, monitors_used: used, monitor_limit: plan.max_monitors, over_limit: used > plan.max_monitors, next_plan: null })

interface Opts { role?: 'owner' | 'member'; verified?: boolean; subscription?: object; usage?: object; path?: string; post?: (url: string, body: unknown) => Promise<Response> }

function setup(o: Opts = {}) {
  const posts: { url: string; body: unknown }[] = []
  const user = { id: 'u', email: 'me@example.com', email_verified_at: o.verified === false ? null : '2026-01-01T00:00:00Z' }
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) return json(200, { user, organizations: [{ id: 'o', name: 'Org', role: o.role ?? 'owner' }] })
    if (init?.method === 'POST') {
      const body = init.body ? JSON.parse(init.body as string) : undefined
      posts.push({ url, body })
      return o.post ? o.post(url, body) : json(200, { url: 'https://checkout.stripe.test/c/1' })
    }
    if (url.includes('/billing/subscription')) return json(200, { subscription: o.subscription ?? sub(), usage: o.usage ?? usage() })
    if (url.includes('/plans')) return json(200, { plans: [free, pro, agency], billing_enabled: true })
    return json(404, {})
  }))
  render(<MemoryRouter initialEntries={[o.path ?? '/billing']}><AuthProvider><BillingPage /></AuthProvider></MemoryRouter>)
  return posts
}

describe('BillingPage', () => {
  it('lets the owner start checkout, sending only the plan name', async () => {
    const posts = setup()
    await userEvent.click(await screen.findByRole('button', { name: 'Choose Pro' }))
    expect(posts).toHaveLength(1)
    expect(posts[0].url).toBe('/api/v1/billing/checkout-session')
    expect(posts[0].body).toEqual({ plan: 'pro' }) // never a price or amount
    expect(redirectTo).toHaveBeenCalledWith('https://checkout.stripe.test/c/1')
  })

  it('shows the free plan limits and no cancel button', async () => {
    setup()
    expect(await screen.findByRole('heading', { name: 'Current plan: Free' })).toBeInTheDocument()
    expect(screen.getByText(/monitors used · checks every 5 min · 7 days of history/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancel subscription' })).not.toBeInTheDocument()
  })

  it('explains when billing is not available and disables upgrades', async () => {
    setup({ subscription: sub({ billing_enabled: false }) })
    expect(await screen.findByText('Paid plans are not available on this server yet.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Choose Pro' })).toBeDisabled()
  })

  it('requires a verified email before upgrading', async () => {
    setup({ verified: false })
    expect(await screen.findByText('Verify your email address to upgrade.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Choose Agency' })).toBeDisabled()
  })

  it('lists team seats on the plan that has them, and only there', async () => {
    setup({})
    expect(await screen.findByText('5 team members')).toBeInTheDocument()
    expect(screen.getAllByText(/team members/)).toHaveLength(1)
  })

  it('shows members a read-only view', async () => {
    setup({ role: 'member' })
    expect(await screen.findByText('Only the organization owner can change the plan.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Choose Pro' })).toBeDisabled()
  })

  it('shows an active paid plan with renewal date, manage and cancel', async () => {
    const posts = setup({ subscription: sub({ plan: pro, paid_access: true, can_manage: true, has_subscription: true, current_period_end: '2026-12-01T00:00:00Z' }), usage: usage(pro, 8) })
    expect(await screen.findByRole('heading', { name: 'Current plan: Pro' })).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText(/Renews on/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Choose Agency' })).not.toBeInTheDocument() // changes go through the portal
    await userEvent.click(screen.getByRole('button', { name: 'Manage billing' }))
    expect(posts[0].url).toBe('/api/v1/billing/portal-session')
    expect(redirectTo).toHaveBeenCalled()
  })

  it('confirms before cancelling and says access continues', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const posts = setup({ subscription: sub({ plan: pro, paid_access: true, can_manage: true, has_subscription: true, current_period_end: '2026-12-01T00:00:00Z' }) })
    const btn = await screen.findByRole('button', { name: 'Cancel subscription' })
    await userEvent.click(btn)
    expect(posts).toHaveLength(0)
    await userEvent.click(btn)
    expect(posts.map((p) => p.url)).toEqual(['/api/v1/billing/cancel-subscription'])
    expect(confirm.mock.calls[0][0]).toMatch(/keep your paid plan until the end of the period/)
  })

  it('shows a pending cancellation with the end date', async () => {
    setup({ subscription: sub({ plan: pro, paid_access: true, can_manage: true, has_subscription: true, cancel_at_period_end: true, current_period_end: '2026-12-01T00:00:00Z' }) })
    expect(await screen.findByText(/You keep full access until then/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancel subscription' })).not.toBeInTheDocument()
  })

  it('warns about a failed payment while access continues', async () => {
    setup({ subscription: sub({ plan: pro, status: 'past_due', paid_access: true, can_manage: true, has_subscription: true }) })
    expect(await screen.findByText(/Payment failed — update your payment method/)).toBeInTheDocument()
  })

  it('explains an over-limit downgrade without deleting anything', async () => {
    setup({ usage: usage(free, 9) })
    expect(await screen.findByRole('alert')).toHaveTextContent(/Nothing has been deleted.*delete 4 or upgrade/)
  })

  it('tells the user a returning checkout is awaiting confirmation, not already active', async () => {
    setup({ path: '/billing?checkout=success' })
    expect(await screen.findByText(/waiting for the payment provider to confirm/)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Current plan: Free' })).toBeInTheDocument()
  })

  it('reports the purchase to Google Ads once the provider confirms it, and only once', async () => {
    const paid = sub({ plan: pro, paid_access: true, current_period_end: '2026-11-30T00:00:00Z', can_manage: true, has_subscription: true })
    setup({ path: '/billing?checkout=success', subscription: paid, usage: usage(pro) })
    await screen.findByRole('heading', { name: 'Current plan: Pro' })
    await vi.waitFor(() => expect(trackPurchase).toHaveBeenCalledTimes(1))
    expect(trackPurchase).toHaveBeenCalledWith(19, '2026-11-30T00:00:00Z')
  })

  it('does not report a purchase for an unpaid return from Checkout or for a customer just opening the page', async () => {
    setup({ path: '/billing?checkout=success' })
    await screen.findByText(/waiting for the payment provider to confirm/)
    expect(trackPurchase).not.toHaveBeenCalled()
  })

  it('does not report anything when a paying customer simply opens the billing page', async () => {
    const paid = sub({ plan: pro, paid_access: true, current_period_end: '2026-11-30T00:00:00Z', can_manage: true, has_subscription: true })
    setup({ subscription: paid, usage: usage(pro) })
    await screen.findByRole('heading', { name: 'Current plan: Pro' })
    expect(trackPurchase).not.toHaveBeenCalled()
  })

  it('acknowledges a cancelled checkout', async () => {
    setup({ path: '/billing?checkout=canceled' })
    expect(await screen.findByText(/You haven’t been charged/)).toBeInTheDocument()
  })

  it('shows provider errors without leaking details', async () => {
    setup({ post: () => json(502, { error: { code: 'BILLING_PROVIDER_ERROR', message: 'The payment provider could not be reached. Please try again.' } }) })
    await userEvent.click(await screen.findByRole('button', { name: 'Choose Pro' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The payment provider could not be reached')
    expect(redirectTo).not.toHaveBeenCalled()
  })
})
