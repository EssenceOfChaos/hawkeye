import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { LandingPage } from './LandingPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const plans = [
  { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7, max_domains: 3 },
  { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90, max_domains: 25 },
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

  it('social icons link to the HawkEye accounts in a new tab', () => {
    renderLanding(() => json(200, { plans: [] }))
    const expected = {
      X: 'https://x.com/HawkEyeObs',
      Facebook: 'https://www.facebook.com/people/HawkEye/61595561066921/',
      LinkedIn: 'https://www.linkedin.com/company/hawkeye-observer/',
    }
    for (const [name, href] of Object.entries(expected)) {
      const link = screen.getByRole('link', { name: `HawkEye on ${name}` })
      expect(link).toHaveAttribute('href', href)
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'))
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

  it('advertises domain expiry warnings, with each plan’s limit, and is honest that not every registry shares the date', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    await screen.findByText('50 website monitors')
    expect(screen.getByRole('heading', { name: 'Domain expiry alerts' })).toBeInTheDocument()
    expect(screen.getByText('Domain expiry warnings for up to 3 domains')).toBeInTheDocument()
    expect(screen.getByText('Domain expiry warnings for up to 25 domains')).toBeInTheDocument()
    expect(screen.getByText('Does HawkEye warn me before my domain name expires?')).toBeInTheDocument()
    expect(screen.getByText(/Not every registry shares this date/)).toBeInTheDocument()
    expect(screen.getByText(/Every plan includes .*domain expiry warnings/)).toBeInTheDocument()
  })

  it('puts the no-false-alarms message above the feature list, and says the true thing about it', () => {
    renderLanding(() => json(200, { plans: [] }))
    const truth = screen.getByRole('heading', { name: 'A failed check is not an incident.' })
    const features = screen.getByRole('heading', { name: 'Everything you need to keep watch.' })
    expect(truth.compareDocumentPosition(features) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const section = truth.closest('section')!
    expect(within(section).getByText(/only after consecutive failed checks/)).toBeInTheDocument()
    expect(within(section).getByText(/verifies its own connection/)).toBeInTheDocument()
    expect(screen.getByText('Outages confirmed before alerts')).toBeInTheDocument()
  })

  it('gives each plan a one-line description that names no unbuilt feature', async () => {
    const all = [
      ...plans,
      { code: 'agency', name: 'Agency', monthly_price_cents: 5900, max_monitors: 200, minimum_check_interval_seconds: 60, history_retention_days: 365 },
    ]
    const { container } = renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans: all, billing_enabled: true }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    expect(await screen.findByText(/Everything you need to watch a personal site/)).toBeInTheDocument()
    expect(screen.getByText(/Faster checks and deeper history/)).toBeInTheDocument()
    expect(screen.getByText(/Room for many client websites/)).toBeInTheDocument()
    const taglines = [...container.querySelectorAll('.plan-tagline')].map((e) => e.textContent).join(' ')
    expect(taglines).not.toMatch(/team|branded|priority|sms|csv|export/i)
  })

  it('shows real product screens, labelled as sample data, before the problem statement', () => {
    const { container } = renderLanding(() => json(200, { plans: [] }))
    const demo = screen.getByRole('heading', { name: 'See it in action.' })
    const problem = screen.getByRole('heading', { name: /Your website doesn’t clock out/ })
    expect(demo.compareDocumentPosition(problem) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText(/shown with sample data/)).toBeInTheDocument()
    const shots = [...container.querySelectorAll('.shot img')]
    expect(shots).toHaveLength(3)
    shots.forEach((img) => {
      expect(img.getAttribute('alt')).toMatch(/sample data/i)
      expect(img).toHaveAttribute('width')
      expect(img).toHaveAttribute('height') // reserved space, so the page does not jump while they load
      expect(img).toHaveAttribute('loading', 'lazy')
    })
  })
})
