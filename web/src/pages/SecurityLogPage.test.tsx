import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { SecurityLogPage } from './SecurityLogPage'
import { describe as sentence, device, type LogEvent } from '../securityLog'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const ev = (o: Partial<LogEvent> = {}): LogEvent => ({
  id: 1, occurred_at: '2026-11-10T09:30:00Z', action: 'monitor.deleted',
  actor: { type: 'user', email: 'owner@example.com', credential_name: null },
  target: { type: 'monitor', id: 'm1', name: 'Shop' }, details: {},
  ip: '203.0.113.9', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
  request_id: 'r1', ...o,
})

function setup(opts: { role?: 'owner' | 'member'; pages?: LogEvent[][] } = {}) {
  const calls: string[] = []
  const pages = opts.pages ?? [[ev()]]
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      calls.push(url)
      if (url.includes('/auth/me')) {
        return json(200, { user: { id: 'u', email: 'owner@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Acme', role: opts.role ?? 'owner' }] })
      }
      if (url.includes('/security-log')) {
        const i = url.includes('before=') ? 1 : 0
        return json(200, { events: pages[i] ?? [], next_before: i === 0 && pages.length > 1 ? 99 : null, categories: ['monitor', 'api_key'], retention_days: 365 })
      }
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <SecurityLogPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('SecurityLogPage', () => {
  it('says who did what, when, and from where, naming the key used', async () => {
    setup({
      pages: [[
        ev(),
        ev({ id: 2, action: 'monitor.paused', actor: { type: 'api_key', email: 'owner@example.com', credential_name: 'Deploy script' }, user_agent: 'curl/8.5.0', ip: null }),
      ]],
    })
    const table = await screen.findByRole('table', { name: 'Security log for Acme' })
    const rows = within(table).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('owner@example.com')
    expect(rows[1]).toHaveTextContent('Deleted the monitor “Shop”')
    expect(rows[1]).toHaveTextContent('203.0.113.9')
    expect(rows[1]).toHaveTextContent('Safari on macOS')
    expect(rows[2]).toHaveTextContent('with the API key “Deploy script”')
    expect(rows[2]).toHaveTextContent('Paused the monitor “Shop”')
    expect(rows[2]).toHaveTextContent('curl/8.5.0')
    expect(rows[2]).toHaveTextContent('Not recorded')
    expect(screen.getByText('Events are kept for 365 days.')).toBeInTheDocument()
  })

  it('filters by category and loads older events', async () => {
    const calls = setup({ pages: [[ev()], [ev({ id: 0, action: 'monitor.created' })]] })
    await screen.findByText('Deleted the monitor “Shop”')
    await userEvent.click(screen.getByRole('button', { name: 'Show older' }))
    expect(await screen.findByText('Created the monitor “Shop”')).toBeInTheDocument()
    expect(calls.some((u) => u.includes('before=99'))).toBe(true)

    await userEvent.selectOptions(screen.getByLabelText('Show'), 'API keys')
    expect(calls.some((u) => u.includes('category=api_key'))).toBe(true)
  })

  it('tells a member that only the owner can see it, without asking the server', async () => {
    const calls = setup({ role: 'member' })
    expect(await screen.findByText(/Only the owner of Acme can see its security log/)).toBeInTheDocument()
    expect(calls.some((u) => u.includes('/security-log'))).toBe(false)
  })
})

describe('security log sentences', () => {
  it('reads well for the trickier events', () => {
    expect(sentence(ev({ action: 'account.signed_in', details: { method: 'recovery_code' } }))).toBe('Signed in with a recovery code')
    expect(sentence(ev({ action: 'monitor.changed', details: { fields: ['check_interval_seconds', 'name'] } }))).toBe('Changed the monitor “Shop” (check interval seconds, name)')
    expect(sentence(ev({ action: 'account.password_changed', details: { other_sessions_ended: 2 } }))).toBe('Changed the password and signed out 2 other sessions')
    expect(sentence(ev({ action: 'monitor.deleted', target: { type: 'monitor', id: 'm', name: null } }))).toBe('Deleted the monitor (name not recorded)')
    expect(sentence(ev({ action: 'something.new' }))).toBe('something.new')
  })

  it('names browsers and tools from their User-Agent', () => {
    expect(device('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0')).toBe('Edge on Windows')
    expect(device('python-requests/2.32.3')).toBe('python-requests/2.32.3')
    expect(device(null)).toBeNull()
  })
})
