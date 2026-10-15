import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { LandingPage } from './LandingPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const plans = [
  { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7 },
  { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90 },
]

function renderLanding(fetchImpl: (url: string) => Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn(fetchImpl))
  return render(
    <MemoryRouter>
      <AuthProvider>
        <LandingPage />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('LandingPage', () => {
  it('shows the brand, pricing from the API and honest availability labels', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    expect(screen.getAllByRole('img', { name: 'HawkEye' }).length).toBeGreaterThan(0)
    expect(await screen.findByText('50 website monitors')).toBeInTheDocument()
    expect(screen.getByText('$19')).toBeInTheDocument()
    // Paid checkout is not built yet, so the page must not pretend it is.
    expect(screen.getByRole('button', { name: /open soon/i })).toBeDisabled()
    expect(screen.getAllByRole('link', { name: /start monitoring free/i }).length).toBeGreaterThan(0)
    // Features that are not built (exports, branded reports, priority support, team seats, SMS) must not be advertised.
    expect(screen.queryByText(/branded|priority support|team members|csv export/i)).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /Always in sight/ })).toBeInTheDocument()
  })

  it('offers real upgrade links once billing is enabled', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans, billing_enabled: true }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    expect(await screen.findByRole('link', { name: /upgrade to Pro/ })).toHaveAttribute('href', '/register')
    expect(screen.queryByRole('button', { name: /open soon/i })).not.toBeInTheDocument()
  })

  it('still offers signup when pricing cannot be loaded', async () => {
    renderLanding(() => Promise.reject(new Error('offline')))
    expect(await screen.findByText(/Pricing is temporarily unavailable/)).toBeInTheDocument()
  })

  it('describes the illustration without claiming it is a real screenshot', () => {
    renderLanding(() => json(200, { plans: [] }))
    expect(screen.getByRole('img', { name: /Illustration of a HawkEye dashboard/ })).toBeInTheDocument()
  })

  it('decorative feature icons are hidden from assistive technology', () => {
    const { container } = renderLanding(() => json(200, { plans: [] }))
    const icons = container.querySelectorAll('.feature img')
    expect(icons.length).toBeGreaterThanOrEqual(6)
    icons.forEach((img) => expect(img).toHaveAttribute('alt', ''))
  })

  it('social links are disabled placeholders, not links to accounts that do not exist', () => {
    renderLanding(() => json(200, { plans: [] }))
    for (const name of ['X', 'LinkedIn', 'GitHub', 'YouTube']) {
      expect(screen.getByRole('button', { name: `${name} (coming soon)` })).toBeDisabled()
    }
  })

  it('footer offers sign up and log in only to visitors who are not signed in', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans: [] }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    const footer = screen.getByRole('contentinfo')
    expect(await within(footer).findByRole('link', { name: 'Sign up' })).toBeInTheDocument()
    expect(within(footer).getByRole('link', { name: 'Log in' })).toBeInTheDocument()
  })

  it('footer hides sign up and log in for a signed-in user', async () => {
    const me = { user: { id: 'u', email: 'a@b.co', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [] }
    renderLanding((url) => (url.includes('/plans') ? json(200, { plans: [] }) : json(200, me)))
    const footer = screen.getByRole('contentinfo')
    expect(await within(footer).findByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
    expect(within(footer).queryByRole('link', { name: 'Sign up' })).not.toBeInTheDocument()
    expect(within(footer).queryByRole('link', { name: 'Log in' })).not.toBeInTheDocument()
  })

  it('advertises what the product really does: website and API checks and SSL certificate warnings, on every plan', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    await screen.findByText('50 website monitors')
    expect(screen.getByRole('heading', { name: 'Website and API monitoring' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'SSL certificate alerts' })).toBeInTheDocument()
    // Every plan card lists them, so visitors can see they are not paid extras.
    expect(screen.getAllByText('SSL certificate expiry warnings')).toHaveLength(plans.length)
    expect(screen.getAllByText('Website and API monitoring').length).toBeGreaterThanOrEqual(plans.length)
    expect(screen.getByText(/Every plan includes website and API monitoring/)).toBeInTheDocument()
    expect(screen.getByText(/Paid plans add maintenance windows/)).toBeInTheDocument()
    expect(screen.getByText('Where can HawkEye send alerts?')).toBeInTheDocument()
    expect(screen.getByText('Does HawkEye warn me before my SSL certificate expires?')).toBeInTheDocument()
    expect(screen.getByText(/Can I monitor an API\?/)).toBeInTheDocument()
  })
})
