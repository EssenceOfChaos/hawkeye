import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { StatusBadge } from '../components/StatusBadge'
import { MaintenancePage } from './MaintenancePage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const win = (o: Record<string, unknown> = {}) => ({
  id: 'w1', name: 'Database upgrade', starts_at: '2026-10-20T02:00:00Z', duration_minutes: 60, repeat: 'none', all_monitors: true, monitor_ids: [],
  status: 'upcoming', next_start: '2026-10-20T02:00:00Z', created_at: '2026-10-01T00:00:00Z', ...o,
})

function setup(opts: { role?: 'owner' | 'member'; windows: ReturnType<typeof win>[]; onPost?: () => Promise<Response> }) {
  document.cookie = 'csrf_token=t; path=/'
  const calls: { m: string; url: string; body?: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({ m: init?.method ?? 'GET', url, body: init?.body as string | undefined })
      if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: opts.role ?? 'owner' }] })
      if (url.endsWith('/maintenance-windows') && init?.method === 'POST') return opts.onPost?.() ?? json(201, { maintenance_window: win() })
      if (url.includes('/maintenance-windows')) return json(200, { maintenance_windows: opts.windows, max: 50 })
      if (url.includes('/monitors')) return json(200, { monitors: [{ id: 'm1', name: 'Shop' }], total: 1 })
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <MaintenancePage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('MaintenancePage', () => {
  it('shows each window’s state in words', async () => {
    setup({ windows: [win(), win({ id: 'w2', name: 'Live now', status: 'active' }), win({ id: 'w3', name: 'Old', status: 'ended', next_start: null })] })
    expect(await screen.findByText('Database upgrade')).toBeInTheDocument()
    expect(screen.getByText(/Scheduled/)).toBeInTheDocument()
    expect(screen.getByText(/In progress/)).toBeInTheDocument()
    expect(screen.getByText(/Finished/)).toBeInTheDocument()
  })

  it('sends the chosen monitors and a UTC start time', async () => {
    const calls = setup({ windows: [] })
    await screen.findByText('No maintenance windows yet.')
    await userEvent.type(screen.getByLabelText('Name'), 'Deploy')
    await userEvent.click(screen.getByLabelText(/All monitors/))
    await userEvent.click(await screen.findByLabelText('Shop'))
    await userEvent.click(screen.getByRole('button', { name: 'Schedule window' }))
    const post = calls.find((c) => c.m === 'POST')
    const body = JSON.parse(post!.body!)
    expect(body).toMatchObject({ name: 'Deploy', duration_minutes: 60, repeat: 'none', all_monitors: false, monitor_ids: ['m1'] })
    expect(body.starts_at).toMatch(/Z$/)
  })

  it('points to the plans page when the plan does not include maintenance windows', async () => {
    setup({
      windows: [],
      onPost: () => json(403, { error: { code: 'PLAN_LIMIT_REACHED', message: 'Maintenance windows are not included in your Free plan.', details: {} } }),
    })
    await screen.findByText('No maintenance windows yet.')
    await userEvent.type(screen.getByLabelText('Name'), 'Deploy')
    await userEvent.click(screen.getByRole('button', { name: 'Schedule window' }))
    expect(await screen.findByText(/not included in your Free plan/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'See plans' })).toHaveAttribute('href', '/billing')
  })

  it('lets members look but not change', async () => {
    setup({ role: 'member', windows: [win()] })
    expect(await screen.findByText('Database upgrade')).toBeInTheDocument()
    expect(screen.getByText(/Only the organization owner/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Delete/ })).not.toBeInTheDocument()
  })
})

describe('StatusBadge', () => {
  it('says Maintenance while a window covers an active monitor, but Paused still wins', () => {
    const { rerender } = render(<StatusBadge monitor={{ enabled: true, current_status: 'down', in_maintenance: true }} />)
    expect(screen.getByText('Maintenance')).toBeInTheDocument()
    rerender(<StatusBadge monitor={{ enabled: false, current_status: 'down', in_maintenance: true }} />)
    expect(screen.getByText('Paused')).toBeInTheDocument()
  })
})
