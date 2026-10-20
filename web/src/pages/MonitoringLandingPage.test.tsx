import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { MonitoringLandingPage } from './MonitoringLandingPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const plans = [
  { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7 },
  { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90, max_regions: 3 },
]

function renderPage(fetchImpl: (url: string) => Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn(fetchImpl))
  return render(
    <MemoryRouter>
      <AuthProvider>
        <MonitoringLandingPage />
      </AuthProvider>
    </MemoryRouter>,
  )
}

const anonymous = (url: string) => (url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }))

describe('MonitoringLandingPage', () => {
  it('leads with the campaign headline and one clear call to action to sign up', async () => {
    renderPage(anonymous)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Know Before Your Customers Do.')
    expect(await screen.findAllByRole('link', { name: 'Start Monitoring Free' })).not.toHaveLength(0)
    for (const link of screen.getAllByRole('link', { name: 'Start Monitoring Free' })) expect(link).toHaveAttribute('href', '/register')
    expect(screen.getByRole('link', { name: 'View Pricing' })).toHaveAttribute('href', '#pricing-h')
  })

  it('has no site navigation to wander off to, and keeps itself out of search results', () => {
    renderPage(anonymous)
    expect(screen.queryByRole('navigation', { name: /product|company/i })).not.toBeInTheDocument()
    expect(document.head.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe('noindex')
  })

  it('shows live plan prices and the multi-location note only when plans offer several locations', async () => {
    renderPage(anonymous)
    expect(await screen.findByText('50 website monitors')).toBeInTheDocument()
    expect(screen.getByText('$19')).toBeInTheDocument()
    expect(screen.getByText(/Checks from several locations/)).toBeInTheDocument()
  })

  it('does not claim several locations when no plan offers them', async () => {
    renderPage((url) => (url.includes('/plans') ? json(200, { plans: [plans[0]] }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })))
    expect(await screen.findByText('5 website monitors')).toBeInTheDocument()
    expect(screen.queryByText(/Checks from several locations/)).not.toBeInTheDocument()
  })

  it('treats the background art as decorative and describes the illustration honestly', () => {
    const { container } = renderPage(anonymous)
    expect(screen.getByRole('img', { name: /Illustration of a HawkEye dashboard/ })).toBeInTheDocument()
    for (const img of container.querySelectorAll('.mon-hero-bg, .mon-earth-bg')) expect(img).toHaveAttribute('alt', '')
  })
})
