import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { BillingPage } from './BillingPage'
import { LandingPage } from './LandingPage'
import { MonitoringLandingPage } from './MonitoringLandingPage'

// The pricing cards on the landing page, the monitoring landing page and the billing page must always say the same thing about each plan.
// Every optional plan field is switched on for the top plan, so a feature added to one page and forgotten on another fails here.

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const free = { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7, max_domains: 3, max_team_members: 1, max_regions: 1 }
const pro = {
  code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90,
  max_domains: 25, max_team_members: 1, max_regions: 3, maintenance_enabled: true, reports_enabled: true,
}
const agency = {
  code: 'agency', name: 'Agency', monthly_price_cents: 5900, max_monitors: 200, minimum_check_interval_seconds: 60, history_retention_days: 365,
  max_domains: 100, max_team_members: 5, max_regions: 3, maintenance_enabled: true, reports_enabled: true, branded_reports_enabled: true,
}
const plans = [free, pro, agency]

const user = { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }
const subscription = { billing_enabled: true, plan: free, status: 'active', paid_access: false, cancel_at_period_end: false, current_period_end: null, can_manage: false, has_subscription: false }
const usage = { plan: free, monitors_used: 1, monitor_limit: 5, over_limit: false, next_plan: null }

function stubApi(signedIn: boolean) {
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (url.includes('/plans')) return json(200, { plans, billing_enabled: true })
    if (!signedIn) return json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
    if (url.includes('/auth/me')) return json(200, { user, organizations: [{ id: 'o', name: 'Org', role: 'owner' }] })
    if (url.includes('/billing/subscription')) return json(200, { subscription, usage })
    return json(404, {})
  }))
}

/** What a visitor reads on each plan card: the tagline, price and every feature line, in order, by plan name. */
function readCards(): Record<string, string[]> {
  const cards: Record<string, string[]> = {}
  for (const card of document.querySelectorAll<HTMLElement>('.plan')) {
    const name = card.querySelector('h3')?.firstChild?.textContent?.trim() ?? '' // the billing page adds a "Current plan" badge after the name
    cards[name] = [
      card.querySelector('.plan-tagline')?.textContent ?? '',
      card.querySelector('.price')?.textContent ?? '',
      ...[...card.querySelectorAll('ul li')].map((li) => li.textContent ?? ''),
    ]
  }
  return cards
}

async function cardsOn(ui: React.ReactElement, signedIn: boolean) {
  stubApi(signedIn)
  const { unmount } = render(<MemoryRouter><AuthProvider>{ui}</AuthProvider></MemoryRouter>)
  await screen.findAllByText('200 website monitors')
  const cards = readCards()
  unmount()
  return cards
}

describe('pricing cards stay in sync', () => {
  it('show the same plans, prices and features on the landing page, the monitoring landing page and the billing page', async () => {
    const landing = await cardsOn(<LandingPage />, false)
    const monitoring = await cardsOn(<MonitoringLandingPage />, false)
    const billing = await cardsOn(<BillingPage />, true)

    expect(Object.keys(landing)).toEqual(['Free', 'Pro', 'Agency'])
    expect(monitoring).toEqual(landing)
    expect(billing).toEqual(landing)
  })

  it('include every plan feature switched on in the plans table, on all three pages', async () => {
    for (const [ui, signedIn] of [[<LandingPage />, false], [<MonitoringLandingPage />, false], [<BillingPage />, true]] as const) {
      const cards = await cardsOn(ui, signedIn)
      expect(cards.Agency).toEqual(expect.arrayContaining([
        '200 website monitors', 'Checks from up to 3 locations', '5 team members', 'Domain expiry warnings for up to 100 domains',
        'Maintenance windows', 'Monthly PDF uptime reports', 'Branded Reports with name and logo',
      ]))
      expect(cards.Free).not.toContain('Branded Reports with name and logo')
    }
  })
})
