import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { StatusPage } from './StatusPage'
import { StatusPageSettings } from './StatusPageSettings'

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
    expect(screen.getByText(/✖ Down/)).toBeInTheDocument()
    expect(screen.getByText(/ongoing/)).toBeInTheDocument()
  })

  it('shows a plain not-found page for an unknown or unpublished address', async () => {
    renderPublic(() => json(404, { error: { code: 'NOT_FOUND', message: 'Status page not found.' } }))
    expect(await screen.findByRole('heading', { name: 'Status page not found' })).toBeInTheDocument()
  })
})

describe('StatusPageSettings', () => {
  function renderSettings(existing: unknown) {
    document.cookie = 'csrf_token=t; path=/'
    const calls: { m: string; url: string; body?: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        calls.push({ m: init?.method ?? 'GET', url, body: init?.body as string | undefined })
        if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: 'owner' }] })
        if (url.endsWith('/status-page') && init?.method === 'PUT') return json(200, { status_page: existing })
        if (url.endsWith('/status-page')) return json(200, { status_page: existing })
        if (url.includes('/monitors')) return json(200, { monitors: [{ id: 'm1', name: 'Website' }, { id: 'm2', name: 'API' }], total: 2 })
        return json(404, {})
      }),
    )
    render(
      <MemoryRouter>
        <AuthProvider>
          <StatusPageSettings />
        </AuthProvider>
      </MemoryRouter>,
    )
    return calls
  }

  it('saves the chosen monitors with their public names', async () => {
    const calls = renderSettings(null)
    await userEvent.type(await screen.findByLabelText('Address'), 'acme')
    await userEvent.type(screen.getByLabelText('Title'), 'Acme status')
    await userEvent.click(screen.getByLabelText('Website'))
    await userEvent.type(screen.getByLabelText('Public name for Website'), 'Marketing site')
    await userEvent.click(screen.getByLabelText(/Publish/))
    await userEvent.click(screen.getByRole('button', { name: 'Save status page' }))
    expect(await screen.findByText(/Your status page is public/)).toBeInTheDocument()
    const put = calls.find((c) => c.m === 'PUT')
    expect(JSON.parse(put!.body!)).toMatchObject({ slug: 'acme', title: 'Acme status', published: true, monitors: [{ monitor_id: 'm1', display_name: 'Marketing site' }] })
  })

  it('loads an existing page and links to it when published', async () => {
    renderSettings({ slug: 'acme', title: 'Acme', description: '', published: true, monitors: [{ monitor_id: 'm2', display_name: '' }] })
    expect(await screen.findByDisplayValue('acme')).toBeInTheDocument()
    expect(screen.getByLabelText('API')).toBeChecked()
    expect(screen.getByLabelText('Website')).not.toBeChecked()
    expect(screen.getByRole('link', { name: /\/status\/acme/ })).toHaveAttribute('href', '/status/acme')
  })
})
