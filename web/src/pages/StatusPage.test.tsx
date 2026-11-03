import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { StatusPage } from './StatusPage'
import { StatusPageEditor, StatusPagesPage } from './StatusPageSettings'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const days = (n: number, status = 'ok') => Array.from({ length: n }, (_, i) => ({ date: `2026-10-${String(i + 1).padStart(2, '0')}`, uptime_percent: status === 'none' ? null : 100, status }))

const page = (o: Record<string, unknown> = {}) => ({
  title: 'Acme status', description: 'Live status', overall: 'operational', history_days: 7, generated_at: '2026-10-15T12:00:00Z',
  monitors: [{ name: 'Website', state: 'up', uptime_percent: 99.95, days: days(7) }], incidents: [], ...o,
})

function renderPublic(res: () => Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn(res))
  render(
    <MemoryRouter initialEntries={['/status/acme']}>
      <Routes>
        <Route path="/status/:slug" element={<StatusPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('public StatusPage', () => {
  it('says in words that everything is operational, with uptime per service', async () => {
    renderPublic(() => json(200, page()))
    expect(await screen.findByText(/All systems operational/)).toBeInTheDocument()
    expect(screen.getByText('Website')).toBeInTheDocument()
    expect(screen.getByText(/99.95% uptime/)).toBeInTheDocument()
    expect(screen.getByText('No incidents in the last 14 days.')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: /last 7 days/ })).toBeInTheDocument()
  })

  it('lists outages and ongoing incidents', async () => {
    renderPublic(() =>
      json(200, page({
        overall: 'partial_outage',
        monitors: [{ name: 'Checkout', state: 'down', uptime_percent: 90, days: days(7, 'down') }],
        incidents: [{ monitor: 'Checkout', started_at: '2026-10-15T10:00:00Z', resolved_at: null }],
      })),
    )
    expect(await screen.findByText(/Some systems are experiencing problems/)).toBeInTheDocument()
    expect(screen.getByText(/✖ Offline/)).toBeInTheDocument()
    expect(screen.getByText(/ongoing/)).toBeInTheDocument()
  })

  it('shows the standard look, with Powered by HawkEye, when the page has no branding', async () => {
    renderPublic(() => json(200, page({ branding: { powered_by: true } })))
    expect(await screen.findByText(/Powered by/)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Back to/ })).not.toBeInTheDocument()
    expect(document.querySelector('.status-logo')).toBeNull()
  })

  it('shows the client’s logo, color and link, readably, and hides Powered by when asked', async () => {
    renderPublic(() => json(200, page({
      branding: { logo_url: '/api/v1/public/status/acme/logo?v=1', accent_color: '#ffeb3b', website_url: 'https://www.acme.example.com/', powered_by: false },
    })))
    expect(await screen.findByRole('link', { name: '← Back to acme.example.com' })).toHaveAttribute('href', 'https://www.acme.example.com/')
    expect(document.querySelector('.status-logo')).toHaveAttribute('src', '/api/v1/public/status/acme/logo?v=1')
    const hero = document.querySelector('.status-hero') as HTMLElement
    expect(hero.style.background).toBe('rgb(255, 235, 59)')
    expect(hero.style.color).toBe('rgb(0, 0, 0)') // black text on pale yellow
    expect(screen.queryByText(/Powered by/)).not.toBeInTheDocument()
  })

  it('never turns a non-https website value into a link', async () => {
    renderPublic(() => json(200, page({ branding: { website_url: 'javascript:alert(1)', powered_by: true } })))
    expect(await screen.findByText(/All systems operational/)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Back to/ })).not.toBeInTheDocument()
  })

  it('shows a plain not-found page for an unknown or unpublished address', async () => {
    renderPublic(() => json(404, { error: { code: 'NOT_FOUND', message: 'Status page not found.' } }))
    expect(await screen.findByRole('heading', { name: 'Status page not found' })).toBeInTheDocument()
  })
})

describe('status page settings', () => {
  const look = { accent_color: '', website_url: '', hide_powered_by: false, has_logo: false, logo_version: 0 }
  const pageA = { id: 'p1', slug: 'client-a', title: 'Client A', description: '', published: true, monitors: [{ monitor_id: 'm2', display_name: '' }], ...look }
  const pageB = { id: 'p2', slug: 'client-b', title: 'Client B', description: '', published: false, monitors: [], ...look }

  function renderAt(path: string, list: { status_pages: unknown[]; max: number } = { status_pages: [], max: 1 }, plan: Record<string, unknown> = {}) {
    document.cookie = 'csrf_token=t; path=/'
    const calls: { m: string; url: string; body?: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        const m = init?.method ?? 'GET'
        calls.push({ m, url, body: init?.body as string | undefined })
        if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: 'owner' }] })
        if (url.endsWith('/status-pages') && m === 'POST') return json(201, { status_page: { ...pageB, id: 'new' } })
        if (url.endsWith('/status-pages')) return json(200, list)
        if (url.endsWith('/status-pages/p1') && m === 'PUT') return json(200, { status_page: pageA })
        if (url.endsWith('/status-pages/p1/logo') && m === 'PUT') return json(200, { status_page: { ...pageA, has_logo: true, logo_version: 7 } })
        if (url.endsWith('/status-pages/p1')) return json(200, { status_page: pageA })
        if (url.endsWith('/usage')) return json(200, { usage: { plan: { code: 'pro', ...plan } } })
        if (url.includes('/monitors')) return json(200, { monitors: [{ id: 'm1', name: 'Website' }, { id: 'm2', name: 'API' }], total: 2 })
        return json(404, {})
      }),
    )
    render(
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/status-pages" element={<StatusPagesPage />} />
            <Route path="/status-pages/new" element={<StatusPageEditor />} />
            <Route path="/status-pages/:pageID" element={<StatusPageEditor />} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>,
    )
    return calls
  }

  it('lists the pages, links the public ones, and stops offering new ones at the plan limit', async () => {
    renderAt('/status-pages', { status_pages: [pageA, pageB], max: 2 })
    expect(await screen.findByText('Client A')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '/status/client-a' })).toHaveAttribute('href', '/status/client-a')
    expect(screen.queryByRole('link', { name: '/status/client-b' })).not.toBeInTheDocument() // a draft is not linked
    expect(screen.getByText('Draft')).toBeInTheDocument()
    expect(screen.getByText(/2 of 2 status pages used/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Upgrade' })).toHaveAttribute('href', '/billing')
    expect(screen.queryByRole('link', { name: 'New status page' })).not.toBeInTheDocument()
  })

  it('offers a new page while there is room', async () => {
    renderAt('/status-pages', { status_pages: [pageA], max: 3 })
    expect(await screen.findByRole('link', { name: 'New status page' })).toHaveAttribute('href', '/status-pages/new')
    expect(screen.getByText(/1 of 3 status pages used/)).toBeInTheDocument()
  })

  it('creates a page with the chosen monitors and public names, then returns to the list', async () => {
    const calls = renderAt('/status-pages/new')
    await userEvent.type(await screen.findByLabelText('Address'), 'client-b')
    await userEvent.type(screen.getByLabelText('Title'), 'Client B')
    await userEvent.click(screen.getByLabelText('Website'))
    await userEvent.type(screen.getByLabelText('Public name for Website'), 'Marketing site')
    await userEvent.click(screen.getByLabelText(/Publish/))
    await userEvent.click(screen.getByRole('button', { name: 'Create status page' }))
    expect(await screen.findByText(/Client B is public at \/status\/client-b/)).toBeInTheDocument()
    const post = calls.find((c) => c.m === 'POST')
    expect(JSON.parse(post!.body!)).toMatchObject({ slug: 'client-b', title: 'Client B', published: true, monitors: [{ monitor_id: 'm1', display_name: 'Marketing site' }] })
  })

  it('loads an existing page, links to it when published, and saves it in place', async () => {
    const calls = renderAt('/status-pages/p1')
    expect(await screen.findByDisplayValue('client-a')).toBeInTheDocument()
    expect(screen.getByLabelText('API')).toBeChecked()
    expect(screen.getByLabelText('Website')).not.toBeChecked()
    expect(screen.getByRole('link', { name: /\/status\/client-a/ })).toHaveAttribute('href', '/status/client-a')
    await userEvent.click(screen.getByRole('button', { name: 'Save status page' }))
    expect(await screen.findByText(/Client A is public/)).toBeInTheDocument()
    expect(calls.some((c) => c.m === 'PUT' && c.url.endsWith('/status-pages/p1'))).toBe(true)
  })

  it('on Pro: sends the color and website, uploads a logo at once, and keeps "Powered by" for Agency', async () => {
    const calls = renderAt('/status-pages/p1', undefined, { status_page_branding: true, status_page_white_label: false })
    await screen.findByDisplayValue('client-a')
    expect(screen.getByLabelText(/Hide “Powered by HawkEye”/)).toBeDisabled()
    expect(screen.getByText('Included in the Agency plan.')).toBeInTheDocument()

    await userEvent.upload(screen.getByLabelText('Upload a logo (PNG or JPEG)'), new File([new Uint8Array([137, 80, 78, 71])], 'logo.png', { type: 'image/png' }))
    expect(await screen.findByText('Logo saved.')).toBeInTheDocument()
    expect(screen.getByAltText('Current logo')).toHaveAttribute('src', '/api/v1/status-pages/p1/logo?v=7')
    expect(calls.find((c) => c.m === 'PUT' && c.url.endsWith('/logo'))).toBeTruthy()

    await userEvent.click(screen.getByLabelText('Use an accent color for the header'))
    expect(screen.getByLabelText('Accent color')).toHaveValue('#1a73e8')
    await userEvent.type(screen.getByLabelText(/Link back to your client’s website/), 'https://www.acme.example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Save status page' }))
    expect(await screen.findByText(/Client A is public/)).toBeInTheDocument()
    const put = calls.find((c) => c.m === 'PUT' && c.url.endsWith('/status-pages/p1'))
    expect(JSON.parse(put!.body!)).toMatchObject({ accent_color: '#1a73e8', website_url: 'https://www.acme.example.com', hide_powered_by: false })
  })

  it('on Free: offers branding as an upgrade and asks to create the page before adding a logo', async () => {
    renderAt('/status-pages/new')
    expect(await screen.findByText(/included in the Pro and Agency plans/)).toBeInTheDocument()
    expect(screen.getByLabelText('Use an accent color for the header')).toBeDisabled()
    expect(screen.getByText(/Create the page first/)).toBeInTheDocument()
  })
})
