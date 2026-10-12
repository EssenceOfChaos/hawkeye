import { render, screen } from '@testing-library/react'
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
    expect(screen.getByRole('img', { name: 'HawkEye' })).toBeInTheDocument()
    expect(await screen.findByText('50 website monitors')).toBeInTheDocument()
    expect(screen.getByText('$19')).toBeInTheDocument()
    // Paid checkout is not built yet, so the page must not pretend it is.
    expect(screen.getByRole('button', { name: /open soon/i })).toBeDisabled()
    expect(screen.getAllByRole('link', { name: /start free|Monitor 5 sites free/i }).length).toBeGreaterThan(0)
    // Reports are not built yet.
    expect(screen.getByText('Coming soon')).toBeInTheDocument()
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
})
