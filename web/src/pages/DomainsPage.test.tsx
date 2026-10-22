import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { DomainsPanel } from '../components/Domains'
import { DomainsPage } from './DomainsPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const inDays = (n: number) => new Date(Date.now() + n * 86_400_000 + 3_600_000).toISOString()

const domain = (o: Record<string, unknown> = {}) => ({
  id: 'd1', name: 'example.com', manual: false, monitor_count: 2, tracked: true, status: 'valid', expires_at: inDays(200), days_remaining: 200,
  source: 'rdap', unavailable_reason: null, checked_at: '2026-10-01T00:00:00Z', lookup_error: null, created_at: '2026-10-01T00:00:00Z', ...o,
})

function setup(opts: { role?: 'owner' | 'member'; domains: ReturnType<typeof domain>[]; limit?: number; onPost?: () => Promise<Response> }) {
  document.cookie = 'csrf_token=t; path=/'
  const calls: { m: string; url: string; body?: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({ m: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: opts.role ?? 'owner' }] })
      if (url.endsWith('/domains') && init?.method === 'POST') return opts.onPost?.() ?? json(201, { domain: domain() })
      if (url.includes('/domains/') && init?.method === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
      if (url.includes('/domains')) return json(200, { domains: opts.domains, limit: opts.limit ?? 3 })
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <DomainsPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('DomainsPage', () => {
  it('shows each domain’s state in words', async () => {
    setup({
      domains: [
        domain(),
        domain({ id: 'd2', name: 'soon.org', status: 'expiring', expires_at: inDays(9), days_remaining: 9 }),
        domain({ id: 'd3', name: 'lapsed.net', status: 'expired', expires_at: inDays(-3), days_remaining: -3 }),
        domain({ id: 'd4', name: 'private.de', status: 'unavailable', expires_at: null, days_remaining: null, unavailable_reason: 'The registry for .de domains does not publish expiry dates.' }),
        domain({ id: 'd5', name: 'new.io', status: 'pending', expires_at: null, days_remaining: null }),
      ],
      limit: 5,
    })
    expect(await screen.findByText('example.com')).toBeInTheDocument()
    expect(screen.getByText(/Valid/)).toBeInTheDocument()
    expect(screen.getByText(/Expiring soon/)).toBeInTheDocument()
    expect(screen.getByText(/Expired/, { selector: '.d-badge' })).toBeInTheDocument()
    expect(screen.getByText('Expiry unavailable')).toBeInTheDocument()
    expect(screen.getByText(/does not publish expiry dates\./, { selector: 'td .hint' })).toBeInTheDocument()
    expect(screen.getByText(/Not looked up yet/)).toBeInTheDocument()
    expect(screen.getByText(/Tracking 5 of 5 domains/)).toBeInTheDocument()
  })

  it('says when a domain is beyond the plan and not looked up', async () => {
    setup({ domains: [domain(), domain({ id: 'd2', name: 'extra.com', tracked: false, status: 'pending', expires_at: null, days_remaining: null })], limit: 1 })
    expect(await screen.findByText('extra.com')).toBeInTheDocument()
    expect(screen.getByText('Not tracked')).toBeInTheDocument()
    expect(screen.getByText(/Tracking 1 of 1 domain included in your plan/)).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'See plans' }).length).toBeGreaterThan(0)
  })

  it('keeps showing the last date when lookups are failing', async () => {
    setup({ domains: [domain({ lookup_error: 'rdap: timeout' })] })
    expect(await screen.findByText(/latest lookup failed/)).toBeInTheDocument()
  })

  it('adds a domain by what the owner pasted', async () => {
    const calls = setup({ domains: [] })
    await screen.findByText(/No domains yet/)
    await userEvent.type(screen.getByLabelText('Domain'), 'https://www.acme.com/shop')
    await userEvent.click(screen.getByRole('button', { name: 'Add domain' }))
    const post = calls.find((c) => c.m === 'POST')
    expect(JSON.parse(post!.body!)).toEqual({ name: 'https://www.acme.com/shop' })
  })

  it('shows a field error for something that is not a registrable domain', async () => {
    setup({
      domains: [],
      onPost: () => json(400, { error: { code: 'VALIDATION_ERROR', message: 'One or more fields are invalid.', details: { fields: { name: 'must be a domain name you can register, such as example.com' } } } }),
    })
    await screen.findByText(/No domains yet/)
    await userEvent.type(screen.getByLabelText('Domain'), 'localhost')
    await userEvent.click(screen.getByRole('button', { name: 'Add domain' }))
    expect(await screen.findByText(/must be a domain name you can register/)).toBeInTheDocument()
  })

  it('points to the plans page when the plan has no room', async () => {
    setup({
      domains: [],
      onPost: () => json(403, { error: { code: 'PLAN_LIMIT_REACHED', message: 'Your Free plan includes up to 3 tracked domains and all of them are in use.', details: {} } }),
    })
    await screen.findByText(/No domains yet/)
    await userEvent.type(screen.getByLabelText('Domain'), 'fourth.io')
    await userEvent.click(screen.getByRole('button', { name: 'Add domain' }))
    expect(await screen.findByText(/all of them are in use/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'See plans' })).toHaveAttribute('href', '/billing')
  })

  it('lets owners remove only the domains they added', async () => {
    vi.stubGlobal('confirm', () => true)
    const calls = setup({ domains: [domain(), domain({ id: 'd2', name: 'mine.org', manual: true, monitor_count: 0 })] })
    await screen.findByText('mine.org')
    expect(screen.getAllByRole('button', { name: /Stop tracking/ })).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'Stop tracking mine.org' }))
    expect(calls.some((c) => c.m === 'DELETE' && c.url.endsWith('/domains/d2'))).toBe(true)
  })

  it('lets members look but not change', async () => {
    setup({ role: 'member', domains: [domain({ manual: true })] })
    expect(await screen.findByText('example.com')).toBeInTheDocument()
    expect(screen.getByText(/Only the organization owner/)).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})

describe('DomainsPanel', () => {
  const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

  it('renders nothing before any domain has a known expiry', () => {
    const { container } = wrap(<DomainsPanel domains={[]} tracked={0} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('reassures when nothing expires within 30 days', () => {
    wrap(<DomainsPanel domains={[]} tracked={3} />)
    expect(screen.getByText(/All 3 tracked domains have more than 30 days left/)).toBeInTheDocument()
  })

  it('lists domains that expire soon or already have', () => {
    wrap(
      <DomainsPanel
        tracked={4}
        domains={[
          { domain_id: 'a', name: 'lapsed.net', expires_at: inDays(-2), days_remaining: -2, status: 'expired' },
          { domain_id: 'b', name: 'soon.org', expires_at: inDays(9), days_remaining: 9, status: 'expiring' },
        ]}
      />,
    )
    const list = screen.getByRole('list')
    expect(within(list).getByText('Expired')).toBeInTheDocument()
    expect(within(list).getByText('9 days')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'All domains' })).toHaveAttribute('href', '/domains')
  })
})
