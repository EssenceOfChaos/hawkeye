import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { Layout } from './Layout'
vi.mock('../analytics', async (orig) => ({ ...(await orig<typeof import('../analytics')>()), trackPurchase: vi.fn() }))
import { trackPurchase } from '../analytics'

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
  vi.mocked(trackPurchase).mockClear()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const EMAIL = 'a-rather-long-address.for-a-small-screen@example-company.co.uk'

function mount(o: { subscription?: object; openIncidents?: number; orgs?: { id: string; name: string; role: string }[] } = {}) {
  const calls: { m: string; url: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({ m: init?.method ?? 'GET', url })
      if (url.includes('/auth/me')) {
        return json(200, { user: { id: 'u', email: EMAIL, email_verified_at: '2026-01-01T00:00:00Z' }, organizations: o.orgs ?? [{ id: 'o', name: 'Org', role: 'owner' }] })
      }
      if (url.includes('/incidents')) return json(200, { total: o.openIncidents ?? 0 })
      if (url.includes('/billing/subscription')) return json(200, { subscription: o.subscription })
      if (url.endsWith('/auth/logout')) return Promise.resolve(new Response(null, { status: 204 }))
      return json(404, {})
    }),
  )
  document.cookie = 'csrf_token=t; path=/'
  const view = render(
    <MemoryRouter initialEntries={['/dashboard']}>
      <AuthProvider>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/dashboard" element={<p>dashboard page</p>} />
            <Route path="/monitors" element={<p>monitors page</p>} />
            <Route path="/team" element={<p>team page</p>} />
            <Route path="/account" element={<p>account page</p>} />
          </Route>
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
  return { ...view, calls }
}

const accountButton = () => screen.findByRole('button', { name: `Account menu for ${EMAIL}` })

describe('the main navigation', () => {
  it('keeps the product pages, Reports included, and leaves account pages to the account menu', async () => {
    mount()
    await accountButton()
    const nav = within(screen.getByRole('navigation', { name: 'Main' }))
    for (const name of ['Dashboard', 'Monitors', 'Incidents', 'Domains', 'Alerts', 'Maintenance', 'Status page', 'Reports']) {
      expect(nav.getByRole('link', { name })).toBeInTheDocument()
    }
    for (const name of ['Team', 'Billing', 'Account settings']) expect(nav.queryByRole('link', { name })).not.toBeInTheDocument()
  })

  it('puts the links behind a menu button on small screens, which opens and closes them', async () => {
    mount()
    const button = await screen.findByRole('button', { name: 'Menu' })
    const nav = screen.getByRole('navigation', { name: 'Main' })
    expect(button).toHaveAttribute('aria-controls', nav.id)
    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(nav).not.toHaveClass('open')
    await userEvent.click(button)
    expect(button).toHaveAttribute('aria-expanded', 'true')
    expect(nav).toHaveClass('open')
    await userEvent.click(button)
    expect(nav).not.toHaveClass('open')
  })

  it('closes the menu with Escape and puts focus back on the button', async () => {
    mount()
    const button = await screen.findByRole('button', { name: 'Menu' })
    await userEvent.click(button)
    await userEvent.keyboard('{Escape}')
    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(button).toHaveFocus()
  })

  it('closes the menu on a click outside it, and when a page is chosen', async () => {
    mount()
    const button = await screen.findByRole('button', { name: 'Menu' })
    const nav = screen.getByRole('navigation', { name: 'Main' })
    await userEvent.click(button)
    await userEvent.click(screen.getByText('dashboard page'))
    expect(nav).not.toHaveClass('open')
    await userEvent.click(button)
    await userEvent.click(within(nav).getByRole('link', { name: 'Monitors' }))
    expect(button).toHaveAttribute('aria-expanded', 'false')
  })

  it('says on the button when incidents are open, since the Incidents badge is hidden with the links', async () => {
    mount({ openIncidents: 2 })
    expect(await screen.findByRole('button', { name: 'Menu (2 open incidents)' })).toBeInTheDocument()
  })

  it('counts open incidents in an oval beside Incidents, and on the menu button while the links are hidden', async () => {
    mount({ openIncidents: 3 })
    const button = await screen.findByRole('button', { name: 'Menu (3 open incidents)' })
    const beside = within(screen.getByRole('link', { name: /Incidents/ })).getByText('3', { selector: '.incident-count' })
    expect(beside).toHaveClass('d-badge')
    expect(beside).toHaveAttribute('aria-hidden', 'true') // the link's own text says "(3 open)"
    expect(within(button).getByText('3')).toHaveClass('incident-count', 'menu-count')
    await userEvent.click(button)
    expect(button.querySelector('.menu-count')).toBeNull() // the links, with their own count, are showing
  })

  it('shows 9+ rather than a wide number, and no count when nothing is open', async () => {
    const busy = mount({ openIncidents: 25 })
    await screen.findByRole('button', { name: 'Menu (25 open incidents)' })
    expect(busy.container.querySelector('nav .incident-count')).toHaveTextContent('9+')
    busy.unmount()
    const quiet = mount({ openIncidents: 0 })
    await screen.findByRole('button', { name: 'Menu' })
    expect(quiet.container.querySelector('.incident-count')).toBeNull()
  })

  it('collapses the links sooner when the organization switcher takes room', async () => {
    const single = mount()
    await accountButton()
    expect(single.container.querySelector('header')).not.toHaveClass('multi-org')
    single.unmount()
    const several = mount({ orgs: [{ id: 'o', name: 'Org', role: 'owner' }, { id: 't', name: 'Team', role: 'member' }] })
    await accountButton()
    expect(several.container.querySelector('header')).toHaveClass('multi-org')
  })
})

describe('the account menu', () => {
  it('shows an icon, not the email address, with the address in a tooltip below it', async () => {
    const { container } = mount()
    const button = await accountButton()
    expect(button.querySelector('svg')).not.toBeNull()
    expect(button).toHaveTextContent('')
    expect(container.querySelector('header')?.textContent ?? '').not.toContain(EMAIL)
    const tip = button.closest('.d-tooltip')
    expect(tip).toHaveAttribute('data-tip', EMAIL)
    expect(tip).toHaveClass('d-tooltip-bottom')
  })

  it('opens to the account pages and Log out, naming who is signed in', async () => {
    mount()
    const button = await accountButton()
    expect(button).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('link', { name: 'Billing' })).not.toBeInTheDocument()
    await userEvent.click(button)
    expect(button).toHaveAttribute('aria-expanded', 'true')
    const panel = within(document.getElementById('account-menu-panel')!)
    expect(panel.getByText(EMAIL)).toBeInTheDocument()
    expect(panel.getByRole('link', { name: 'Account settings' })).toHaveAttribute('href', '/account')
    expect(panel.getByRole('link', { name: 'Team' })).toHaveAttribute('href', '/team')
    expect(panel.queryByRole('link', { name: 'Reports' })).not.toBeInTheDocument() // a product page, in the main links
    expect(panel.getByRole('link', { name: 'Billing' })).toHaveAttribute('href', '/billing')
    expect(panel.getByRole('button', { name: 'Log out' })).toBeInTheDocument()
    // The address is in the menu now, so the tooltip would only repeat it on top of the menu.
    expect(button.closest('.d-tooltip')).toHaveAttribute('data-tip', '')
  })

  it('goes to the chosen page and closes', async () => {
    mount()
    await userEvent.click(await accountButton())
    await userEvent.click(screen.getByRole('link', { name: 'Team' }))
    expect(await screen.findByText('team page')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Billing' })).not.toBeInTheDocument()
  })

  it('closes with Escape (focus returns to the icon) and on a click outside', async () => {
    mount()
    const button = await accountButton()
    await userEvent.click(button)
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('link', { name: 'Billing' })).not.toBeInTheDocument()
    expect(button).toHaveFocus()
    await userEvent.click(button)
    await userEvent.click(screen.getByText('dashboard page'))
    expect(screen.queryByRole('link', { name: 'Billing' })).not.toBeInTheDocument()
  })

  it('closes when keyboard focus leaves it, but not when focus moves inside it', async () => {
    mount()
    const button = await accountButton()
    await userEvent.click(button)
    await userEvent.tab()
    expect(screen.getByRole('link', { name: 'Account settings' })).toHaveFocus()
    expect(screen.getByRole('link', { name: 'Billing' })).toBeInTheDocument()
    await userEvent.tab() // Team
    await userEvent.tab() // Billing
    await userEvent.tab() // Log out
    expect(screen.getByRole('button', { name: 'Log out' })).toHaveFocus()
    await userEvent.tab() // out of the menu
    expect(screen.queryByRole('link', { name: 'Billing' })).not.toBeInTheDocument()
  })

  it('logs out from the menu', async () => {
    const { calls } = mount()
    await userEvent.click(await accountButton())
    await userEvent.click(screen.getByRole('button', { name: 'Log out' }))
    await vi.waitFor(() => expect(calls.some((c) => c.m === 'POST' && c.url.endsWith('/auth/logout'))).toBe(true))
  })
})

describe('a purchase confirmed after the buyer left the billing page', () => {
  const paid = { plan: { code: 'pro', name: 'Pro', monthly_price_cents: 1900 }, paid_access: true, current_period_end: '2026-11-30T00:00:00Z' }

  it('is reported to Google Ads on the next page they open', async () => {
    localStorage.setItem('hawkeye_purchase_pending', String(Date.now()))
    mount({ subscription: paid })
    await screen.findByText('dashboard page')
    await vi.waitFor(() => expect(trackPurchase).toHaveBeenCalledWith(19, '2026-11-30T00:00:00Z'))
  })

  it('costs nothing for everyone else: the subscription is not even requested', async () => {
    const { calls } = mount({ subscription: paid })
    await screen.findByText('dashboard page')
    expect(calls.some((c) => c.url.includes('/billing/subscription'))).toBe(false)
    expect(trackPurchase).not.toHaveBeenCalled()
  })
})
