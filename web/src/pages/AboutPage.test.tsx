import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../AuthProvider'
import { AboutPage } from './AboutPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const anonymous = () => json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
const me = { user: { id: 'u', email: 'a@b.co', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [] }

function renderPage(fetchImpl: (url: string) => Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn(fetchImpl))
  return render(
    <MemoryRouter initialEntries={['/about']}>
      <AuthProvider>
        <AboutPage />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('AboutPage', () => {
  it('has one main heading and the sections in order', () => {
    renderPage(anonymous)
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Monitoring should be powerful. Not complicated.')
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'Great software doesn’t need to be complicated.',
      'Three principles. Everything we build.',
      'Your monitoring suite. One click away.',
      'Built on technology we believe in.',
      'Small team. Big ambitions.',
      'Always watching. Always improving.',
      'You focus on building.We’ll keep watch.',
    ])
  })

  it('sends visitors to sign up, and to the features and pricing on the home page', () => {
    renderPage(anonymous)
    const signUp = screen.getAllByRole('link', { name: 'Create Your Free Account' })
    expect(signUp).toHaveLength(2)
    for (const link of signUp) expect(link).toHaveAttribute('href', '/register')
    expect(screen.getByRole('link', { name: 'Explore Features' })).toHaveAttribute('href', '/#features-h')
    expect(screen.getByRole('link', { name: 'Explore Pricing' })).toHaveAttribute('href', '/#pricing-h')
  })

  it('sends a signed-in visitor to the dashboard instead', async () => {
    renderPage((url) => (url.includes('/auth/me') ? json(200, me) : anonymous()))
    expect(await screen.findAllByRole('link', { name: 'Open dashboard' })).toHaveLength(3) // header, hero and closing call to action
    expect(screen.queryByRole('link', { name: 'Create Your Free Account' })).not.toBeInTheDocument()
  })

  it('describes what setup from a domain really looks for, and the OpenAPI and Postman import', () => {
    renderPage(anonymous)
    const setup = screen.getByRole('heading', { name: 'Set up from your domain' }).closest('article')!
    for (const found of ['Your website:', 'Health and API endpoints:', 'SSL certificates and domains:', 'Mail (MX) records:']) {
      expect(within(setup).getByText(found)).toBeInTheDocument()
    }
    // Discovery suggests MX records only; it does not look at A or CNAME records.
    expect(setup).not.toHaveTextContent(/CNAME|\bA records?\b/)
    const importPanel = screen.getByRole('heading', { name: /API documentation into actionable health checks/ }).closest('article')!
    expect(importPanel).toHaveTextContent(/Swagger\/OpenAPI/)
    expect(importPanel).toHaveTextContent(/Postman collections/)
  })

  it('names the stack HawkEye really runs on, and not Redis, which it does not use', () => {
    renderPage(anonymous)
    const stack = screen.getByRole('heading', { name: 'Built on technology we believe in.' }).closest('section')!
    expect(within(stack).getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual(['Go', 'PostgreSQL', 'React'])
    expect(document.body).not.toHaveTextContent(/redis/i)
  })

  it('makes no claims it cannot back: no certifications, customer counts or testimonials', () => {
    renderPage(anonymous)
    expect(document.body).not.toHaveTextContent(/SOC ?2|ISO ?27001|GDPR[- ]certified|HIPAA|customers trust|testimonial|\d[\d,]* (customers|users|teams)/i)
  })

  it('keeps decorative images out of the way of screen readers', () => {
    const { container } = renderPage(anonymous)
    const decorative = [...container.querySelectorAll('main img')].filter((img) => !img.closest('a'))
    expect(decorative.length).toBeGreaterThanOrEqual(8) // two backgrounds, three principle icons, three logos, Scout
    for (const img of decorative) expect(img).toHaveAttribute('alt', '')
    expect(screen.getAllByRole('img', { name: 'HawkEye' }).length).toBeGreaterThan(0)
  })

  it('uses the site footer, which links back to this page', () => {
    renderPage(anonymous)
    const company = within(screen.getByRole('contentinfo')).getByRole('navigation', { name: 'Company' })
    expect(within(company).getByRole('link', { name: 'About us' })).toHaveAttribute('href', '/about')
    const product = within(screen.getByRole('contentinfo')).getByRole('navigation', { name: 'Product' })
    expect(within(product).getByRole('link', { name: 'Pricing' })).toHaveAttribute('href', '/#pricing-h')
  })
})
