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

  it('shows a plain not-found page for an unknown or unpublished address', async () => {
    renderPublic(() => json(404, { error: { code: 'NOT_FOUND', message: 'Status page not found.' } }))
    expect(await screen.findByRole('heading', { name: 'Status page not found' })).toBeInTheDocument()
  })
})

describe('status page settings', () => {
  const pageA = { id: 'p1', slug: 'client-a', title: 'Client A', description: '', published: true, monitors: [{ monitor_id: 'm2', display_name: '' }] }
  const pageB = { id: 'p2', slug: 'client-b', title: 'Client B', description: '', published: false, monitors: [] }

  function renderAt(path: string, list: { status_pages: unknown[]; max: number } = { status_pages: [], max: 1 }) {
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
        if (url.endsWith('/status-pages/p1')) return json(200, { status_page: pageA })
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
})
