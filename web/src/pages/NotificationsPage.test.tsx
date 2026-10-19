import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { NotificationsPage } from './NotificationsPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const mk = (id: string, destination: string, o: Partial<{ verified: boolean; enabled: boolean; user_id: string | null }> = {}) => ({
  id, destination, channel: 'email', user_id: null, enabled: true, verified: true, created_at: '2026-01-01T00:00:00Z', ...o,
})

function setup(opts: { role?: 'owner' | 'member'; emailVerified?: boolean; items: ReturnType<typeof mk>[]; onPost?: () => Promise<Response> }) {
  document.cookie = 'csrf_token=t; path=/'
  const user = { id: 'u', email: 'me@example.com', email_verified_at: opts.emailVerified === false ? null : '2026-01-01T00:00:00Z' }
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url.includes('/auth/me')) return json(200, { user, organizations: [{ id: 'o', name: 'Org', role: opts.role ?? 'owner' }] })
      if (url.endsWith('/notification-settings') && init?.method === 'POST') return opts.onPost?.() ?? json(201, { notification_setting: mk('new', 'x@example.com', { verified: false }) })
      if (url.includes('/notification-settings')) return json(200, { notification_settings: opts.items, max: 10 })
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <NotificationsPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('NotificationsPage', () => {
  it('shows each recipient state in words, not colour alone', async () => {
    setup({ items: [mk('1', 'me@example.com', { user_id: 'u' }), mk('2', 'client@example.com', { verified: false }), mk('3', 'off@example.com', { enabled: false })] })
    expect(await screen.findByText('Receiving alerts')).toBeInTheDocument()
    expect(screen.getByText('Waiting for confirmation')).toBeInTheDocument()
    expect(screen.getByText('Alerts off')).toBeInTheDocument()
    expect(screen.getByText('(you)')).toBeInTheDocument()
  })

  it('blocks adding recipients until the account email is verified', async () => {
    setup({ emailVerified: false, items: [mk('1', 'me@example.com', { user_id: 'u', verified: false })] })
    expect(await screen.findByText(/Verify your own email address first/)).toBeInTheDocument()
    expect(screen.getByLabelText('Email address')).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add recipient' })).toBeDisabled()
  })

  it('explains that a new address must confirm before receiving alerts', async () => {
    const calls = setup({ items: [mk('1', 'me@example.com', { user_id: 'u' })] })
    await screen.findByText('me@example.com')
    await userEvent.type(screen.getByLabelText('Email address'), 'x@example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Add recipient' }))
    expect(await screen.findByText(/We emailed x@example.com a confirmation link/)).toBeInTheDocument()
    expect(calls.some((c) => c.startsWith('POST') && c.endsWith('/notification-settings'))).toBe(true)
  })

  it('shows server field errors for the address', async () => {
    setup({
      items: [mk('1', 'me@example.com', { user_id: 'u' })],
      onPost: () => json(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { destination: 'must be a valid email address' } } } }),
    })
    await screen.findByText('me@example.com')
    await userEvent.type(screen.getByLabelText('Email address'), 'x@example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Add recipient' }))
    expect(await screen.findByText('must be a valid email address')).toBeInTheDocument()
  })

  it('lets members view but not change recipients', async () => {
    setup({ role: 'member', items: [mk('1', 'me@example.com', { user_id: 'u' })] })
    expect(await screen.findByText('me@example.com')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText(/Only the organization owner/)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /Remove/ })).not.toBeInTheDocument()
  })

  it('asks for confirmation before removing a recipient', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const calls = setup({ items: [mk('1', 'me@example.com', { user_id: 'u' })] })
    const row = (await screen.findByText('me@example.com')).closest('tr')!
    await userEvent.click(within(row).getByRole('button', { name: /Remove me@example.com/ }))
    expect(confirm).toHaveBeenCalled()
    expect(calls.some((c) => c.startsWith('DELETE'))).toBe(false)
    confirm.mockRestore()
  })

  it('adds a webhook, shows the signing secret once, and offers a test message', async () => {
    const calls = setup({
      items: [mk('1', 'me@example.com', { user_id: 'u' }), { ...mk('2', 'hooks.example.com/…'), channel: 'webhook' }],
      onPost: () => json(201, { notification_setting: { ...mk('new', 'hooks.example.com/…'), channel: 'webhook' }, signing_secret: 'whsec_abc' }),
    })
    await screen.findByText('me@example.com')
    expect(screen.getAllByRole('button', { name: 'Send test alert' })).toHaveLength(2) // the confirmed email address and the webhook
    await userEvent.selectOptions(screen.getByLabelText('Channel'), 'webhook')
    await userEvent.type(screen.getByLabelText('Webhook URL'), 'https://example.com/hook')
    await userEvent.click(screen.getByRole('button', { name: 'Add destination' }))
    expect(await screen.findByText('whsec_abc')).toBeInTheDocument()
    expect(screen.getByText(/not shown again/)).toBeInTheDocument()
    expect(calls.some((c) => c.startsWith('POST') && c.endsWith('/notification-settings'))).toBe(true)
  })

  it('asks for a Slack URL when Slack is chosen', async () => {
    setup({ items: [mk('1', 'me@example.com', { user_id: 'u' })] })
    await screen.findByText('me@example.com')
    await userEvent.selectOptions(screen.getByLabelText('Channel'), 'slack')
    expect(screen.getByLabelText('Slack incoming webhook URL')).toBeInTheDocument()
  })

  describe('sending a test alert', () => {
    it('emails a confirmed recipient and says where to look', async () => {
      const calls = setup({ items: [mk('1', 'me@example.com', { user_id: 'u' }), mk('2', 'ops@example.com')] })
      await screen.findByText('ops@example.com')
      const row = screen.getByText('ops@example.com').closest('tr')!
      await userEvent.click(within(row).getByRole('button', { name: 'Send test alert' }))
      expect(await screen.findByText(/A test alert was sent to ops@example.com/)).toBeInTheDocument()
      expect(screen.getByText(/check the spam folder/)).toBeInTheDocument()
      expect(calls.some((c) => c === 'POST /api/v1/notification-settings/2/test')).toBe(true)
      expect(calls.filter((c) => c.endsWith('/test'))).toHaveLength(1) // only that recipient
    })

    it('does not offer a test to an address that has not confirmed yet', async () => {
      setup({ items: [mk('1', 'me@example.com', { user_id: 'u' }), mk('2', 'client@example.com', { verified: false })] })
      await screen.findByText('client@example.com')
      const row = screen.getByText('client@example.com').closest('tr')!
      expect(within(row).queryByRole('button', { name: /Send test alert/ })).not.toBeInTheDocument()
      expect(within(row).getByRole('button', { name: 'Resend confirmation' })).toBeInTheDocument()
    })

    it('warns when the recipient is turned off, since real alerts would not reach it', async () => {
      setup({ items: [mk('1', 'me@example.com', { user_id: 'u' }), mk('2', 'off@example.com', { enabled: false })] })
      await screen.findByText('off@example.com')
      const row = screen.getByText('off@example.com').closest('tr')!
      await userEvent.click(within(row).getByRole('button', { name: 'Send test alert' }))
      expect(await screen.findByText(/Alerts are turned off for this recipient/)).toBeInTheDocument()
    })

    it('shows the server’s message when too many tests were sent to one address', async () => {
      setup({
        items: [mk('1', 'me@example.com', { user_id: 'u' })],
        onPost: () => json(429, { error: { code: 'RATE_LIMITED', message: 'Too many requests. Try again later.' } }),
      })
      await screen.findByText('me@example.com')
      // the test POST goes to /test, which the shared mock does not special-case: simulate the limit on that route
      vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
        if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: 'owner' }] })
        if (url.endsWith('/test') && init?.method === 'POST') return json(429, { error: { code: 'RATE_LIMITED', message: 'Too many requests. Try again later.' } })
        return json(200, { notification_settings: [mk('1', 'me@example.com', { user_id: 'u' })], max: 10 })
      }))
      await userEvent.click(screen.getByRole('button', { name: 'Send test alert' }))
      expect(await screen.findByRole('alert')).toHaveTextContent('Too many requests')
    })

    it('shows no test buttons to members, who may only view recipients', async () => {
      setup({ role: 'member', items: [mk('1', 'me@example.com', { user_id: 'u' })] })
      await screen.findByText('me@example.com')
      expect(screen.queryByRole('button', { name: /Send test alert/ })).not.toBeInTheDocument()
    })
  })
})
