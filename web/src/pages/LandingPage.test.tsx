import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../AuthProvider'
import { LandingPage } from './LandingPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const plans = [
  { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7, max_domains: 3, max_team_members: 1 },
  { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90, max_domains: 25, max_team_members: 1 },
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
  it('advertises team seats only on a plan that has more than one, with the number from the plans table', async () => {
    const withSeats = [plans[0], plans[1], { code: 'agency', name: 'Agency', monthly_price_cents: 5900, max_monitors: 200, minimum_check_interval_seconds: 60, history_retention_days: 365, max_domains: 100, max_team_members: 5 }]
    renderLanding((url) => (url.includes('/plans') ? json(200, { plans: withSeats }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })))
    await screen.findByText('200 monitors')
    const cards = document.querySelector('ul.plans') as HTMLElement
    expect(within(cards).getAllByText(/team members/)).toHaveLength(1)
    expect(within(cards).getByText('5 team members, with two-factor sign-in you can require')).toBeInTheDocument()
    expect([...cards.querySelectorAll('.plan-plus')].map((e) => e.textContent)).toEqual(['Everything in Pro, plus:'])
  })

  it('lists reports on a plan only when that plan includes them', async () => {
    const withReports = [
      plans[0],
      { ...plans[1], reports_enabled: true },
      { code: 'agency', name: 'Agency', monthly_price_cents: 5900, max_monitors: 200, minimum_check_interval_seconds: 60, history_retention_days: 365, max_domains: 100, reports_enabled: true, branded_reports_enabled: true },
    ]
    renderLanding((url) => (url.includes('/plans') ? json(200, { plans: withReports }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })))
    await screen.findByText('200 monitors')
    // Each card names only what its plan adds: Pro adds reports, Agency adds the branding on them.
    const cards = document.querySelector('ul.plans') as HTMLElement
    expect(within(cards).getAllByText('Monthly PDF uptime reports')).toHaveLength(1)
    expect(within(cards).getAllByText('Reports with your name and logo')).toHaveLength(1)
  })

  it('shows the brand, pricing from the API and honest availability labels', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    expect(screen.getAllByRole('img', { name: 'HawkEye' }).length).toBeGreaterThan(0)
    expect(await screen.findByText('50 monitors')).toBeInTheDocument()
    expect(within(document.querySelector('ul.plans') as HTMLElement).getByText('$19')).toBeInTheDocument()
    // Paid checkout is not built yet, so the page must not pretend it is.
    expect(screen.getByRole('button', { name: /open soon/i })).toBeDisabled()
    expect(screen.getAllByRole('link', { name: 'Create free account' }).length).toBeGreaterThan(0)
    // Features that are not built (CSV exports, priority support, SMS) must not be advertised, and neither may team seats on plans that are for one person.
    expect(screen.queryByText(/priority support|team members|csv export/i)).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /Always in sight/ })).toBeInTheDocument()
  })

  it('offers real upgrade links once billing is enabled', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans, billing_enabled: true }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    expect(await screen.findByRole('link', { name: 'Sign up, then choose Pro' })).toHaveAttribute('href', '/register')
    expect(screen.queryByRole('button', { name: /open soon/i })).not.toBeInTheDocument()
  })

  it('never words the free plan like a trial that runs out', async () => {
    renderLanding((url) => (url.includes('/plans') ? json(200, { plans, billing_enabled: true }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })))
    await screen.findByText('50 monitors')
    expect(document.body).not.toHaveTextContent(/start (monitoring )?(for )?free|free trial/i)
    const freeCard = screen.getByRole('heading', { name: 'Free', level: 3 }).closest('.plan')!
    expect(within(freeCard as HTMLElement).getByRole('link', { name: 'Create free account' })).toHaveAttribute('href', '/register')
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

  it('gives every feature card its own icon, including the locations card', async () => {
    const { container } = renderLanding(() => json(200, { plans: [{ code: 'pro', max_regions: 3 }] }))
    await screen.findByRole('heading', { name: 'Checks from several locations' })
    const icons = Array.from(container.querySelectorAll('.feature img')).map((img) => img.getAttribute('src'))
    expect(icons.length).toBeGreaterThanOrEqual(13)
    expect(new Set(icons).size).toBe(icons.length)
  })

  it('gives every audience card its own icon', () => {
    const { container } = renderLanding(() => json(200, { plans: [] }))
    const icons = Array.from(container.querySelectorAll('.audiences .feature img')).map((img) => img.getAttribute('src'))
    expect(icons).toHaveLength(4)
    expect(new Set(icons).size).toBe(4)
    expect(container.querySelectorAll('.audience-head img + h3')).toHaveLength(4) // the name sits beside its icon
  })

  it('social icons link to the HawkEye accounts in a new tab', () => {
    renderLanding(() => json(200, { plans: [] }))
    const expected = {
      X: 'https://x.com/HawkEyeOnX',
      Bluesky: 'https://bsky.app/profile/hawkeye-observer.bsky.social',
      Facebook: 'https://www.facebook.com/people/HawkEye/61595561066921/',
      LinkedIn: 'https://www.linkedin.com/company/hawkeye-observer/',
      Reddit: 'https://www.reddit.com/r/HawkEye_Observer/',
      Telegram: 'https://t.me/hawkeye_community',
    }
    for (const [name, href] of Object.entries(expected)) {
      const link = screen.getByRole('link', { name: `HawkEye on ${name}` })
      expect(link).toHaveAttribute('href', href)
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'))
    }
  })

  it('footer links to the free tools, in the Product column, for everyone', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans: [] }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    const product = within(screen.getByRole('contentinfo')).getByRole('navigation', { name: 'Product' })
    expect(within(product).getByRole('link', { name: 'Free tools' })).toHaveAttribute('href', '/tools')
  })

  it('footer offers sign up and log in only to visitors who are not signed in', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans: [] }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    const footer = screen.getByRole('contentinfo')
    expect(await within(footer).findByRole('link', { name: 'Sign up' })).toBeInTheDocument()
    expect(within(footer).getByRole('link', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('footer hides sign up and log in for a signed-in user', async () => {
    const me = { user: { id: 'u', email: 'a@b.co', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [] }
    renderLanding((url) => (url.includes('/plans') ? json(200, { plans: [] }) : json(200, me)))
    const footer = screen.getByRole('contentinfo')
    expect(await within(footer).findByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
    expect(within(footer).queryByRole('link', { name: 'Sign up' })).not.toBeInTheDocument()
    expect(within(footer).queryByRole('link', { name: 'Sign in' })).not.toBeInTheDocument()
  })

  it('advertises what the product really does: website and API checks and SSL certificate warnings, on every plan', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    await screen.findByText('50 monitors')
    expect(screen.getByRole('heading', { name: 'Website and API monitoring' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'DNS monitoring' })).toBeInTheDocument()
    expect(screen.getByText('Cron job and DNS monitoring', { selector: '.value-strip li' })).toBeInTheDocument()
    expect(screen.getByText('Multi-step API checks', { selector: '.value-strip li' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'SSL certificate alerts' })).toBeInTheDocument()
    // Said once under the cards, so visitors can see they are not paid extras.
    const included = screen.getByRole('heading', { name: 'Every plan includes' }).parentElement!
    expect(within(included).getByText('Website, API, cron job and DNS monitoring')).toBeInTheDocument()
    expect(within(included).getByText('SSL certificate and domain expiry warnings')).toBeInTheDocument()
    expect(screen.getByText('Where can HawkEye send alerts?')).toBeInTheDocument()
    expect(screen.getByText('Does HawkEye warn me before my SSL certificate expires?')).toBeInTheDocument()
    expect(screen.getByText(/Can I monitor an API\?/)).toBeInTheDocument()
  })

  it('advertises domain expiry warnings, with each plan’s limit, and is honest that not every registry shares the date', async () => {
    renderLanding((url) =>
      url.includes('/plans') ? json(200, { plans }) : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } }),
    )
    await screen.findByText('50 monitors')
    expect(screen.getByRole('heading', { name: 'Domain expiry alerts' })).toBeInTheDocument()
    const row = screen.getByRole('rowheader', { name: 'Domains watched for expiry' }).closest('tr')!
    expect([...row.querySelectorAll('td')].map((td) => td.textContent)).toEqual(plans.map((p) => String(p.max_domains)))
    expect(screen.getByText('Does HawkEye warn me before my domain name expires?')).toBeInTheDocument()
    expect(screen.getByText(/Not every registry shares this date/)).toBeInTheDocument()
    expect(screen.getByText('SSL certificate and domain expiry warnings')).toBeInTheDocument()
  })

  it('advertises AI root cause analysis among the features, and no longer the dashboard', () => {
    renderLanding(() => json(200, { plans: [] }))
    const card = screen.getByRole('heading', { name: /^Root cause analysis, powered\sby\sAI$/ }).closest('li')!
    expect(within(card).getByText('Know what broke before you open a browser.')).toBeInTheDocument()
    expect(within(card).getByText(/suggests the likely cause and the first thing to check/)).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'One dashboard, clear reports' })).not.toBeInTheDocument()
  })

  it('puts the no-false-alarms message above the feature list, and says the true thing about it', () => {
    renderLanding(() => json(200, { plans: [] }))
    const truth = screen.getByRole('heading', { name: 'A failed check is not an incident.' })
    const features = screen.getByRole('heading', { name: 'Everything you need to keep watch.' })
    expect(truth.compareDocumentPosition(features) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const section = truth.closest('section')!
    expect(within(section).getByText(/checked again 15 seconds later, and a site counts as offline only when that fails too/)).toBeInTheDocument()
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

  it('shows HawkEye in Claude and Codex, labeled as sample data, with links to set each up', () => {
    renderLanding(() => json(200, { plans: [] }))
    const section = screen.getByRole('heading', { name: 'Ask Claude or Codex how your sites are doing.' }).closest('section')!
    expect(within(section).getByText('Codex')).toBeInTheDocument()
    expect(within(section).getByText('Claude Code')).toBeInTheDocument()
    expect(within(section).getByText('What’s the state of my HawkEye monitors?')).toBeInTheDocument()
    expect(within(section).getByText('Why is my website running slow?')).toBeInTheDocument()
    expect(within(section).getByText('Shown with sample data.')).toBeInTheDocument()
    expect(within(section).getByRole('link', { name: 'Set up with Claude' })).toHaveAttribute('href', '/docs/claude')
    expect(within(section).getByRole('link', { name: 'Set up with Codex' })).toHaveAttribute('href', '/docs/codex')
    expect(screen.queryByRole('heading', { name: /clock out/ })).not.toBeInTheDocument()
  })

  it('shows real product screens, labeled as sample data, before the AI assistant section', () => {
    const { container } = renderLanding(() => json(200, { plans: [] }))
    const demo = screen.getByRole('heading', { name: 'See it in action.' })
    const agents = screen.getByRole('heading', { name: 'Ask Claude or Codex how your sites are doing.' })
    expect(demo.compareDocumentPosition(agents) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
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
