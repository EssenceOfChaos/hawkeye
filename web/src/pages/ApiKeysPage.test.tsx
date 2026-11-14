import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { ApiKeysPage } from './ApiKeysPage'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const key = (o: Record<string, unknown> = {}) => ({
  id: 'k1', name: 'Grafana', scope: 'read', prefix: 'hk_abcdefg', owner_email: 'me@example.com', mine: true,
  created_at: '2026-10-01T00:00:00Z', last_used_at: null, expires_at: '2027-01-01T00:00:00Z', expired: false, ...o,
})

function setup(opts: { role?: 'owner' | 'member'; keys?: ReturnType<typeof key>[]; write?: boolean; verified?: boolean; disabled?: boolean } = {}) {
  document.cookie = 'csrf_token=t; path=/'
  const calls: { m: string; url: string; body?: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      const m = init?.method ?? 'GET'
      calls.push({ m, url, body: init?.body as string | undefined })
      if (url.includes('/auth/me')) {
        return json(200, {
          user: { id: 'u', email: 'me@example.com', email_verified_at: opts.verified === false ? null : '2026-01-01T00:00:00Z' },
          organizations: [{ id: 'o', name: 'Acme', role: opts.role ?? 'owner' }],
        })
      }
      if (url.includes('/usage')) {
        const plan = { code: opts.write ? 'pro' : 'free', name: opts.write ? 'Pro' : 'Free', api_write_enabled: Boolean(opts.write) }
        return json(200, { usage: { plan, monitors_used: 0, monitor_limit: 5, over_limit: false, next_plan: null } })
      }
      if (url.endsWith('/api-keys') && m === 'POST') return json(201, { key: key({ id: 'k2', name: 'New one' }), token: 'hk_secret-token-value' })
      if (url.includes('/api-keys/') && m === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
      if (url.endsWith('/api-keys')) {
        if (opts.disabled) return json(503, { error: { code: 'API_KEYS_DISABLED', message: 'off' } })
        return json(200, { keys: opts.keys ?? [], max_keys: 25 })
      }
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <ApiKeysPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('ApiKeysPage', () => {
  it('lists keys without their tokens, and who made them for the owner', async () => {
    setup({ keys: [key(), key({ id: 'k2', name: 'CI', scope: 'write', mine: false, owner_email: 'dev@example.com', expires_at: null })] })
    const row = (await screen.findByText('CI')).closest('tr')!
    expect(within(row).getByText('Read and write')).toBeInTheDocument()
    expect(within(row).getByText('dev@example.com')).toBeInTheDocument()
    expect(within(row).getByText('Never')).toBeInTheDocument()
    expect(screen.getAllByText('hk_abcdefg…', { selector: 'code' })).toHaveLength(2)
    expect(document.body.textContent).not.toContain('hk_secret')
  })

  it('creates a read-only key with the 90-day default and shows the token once', async () => {
    const calls = setup()
    await screen.findByText('No API keys yet.')
    await userEvent.type(screen.getByLabelText('Name'), 'New one')
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }))
    expect(await screen.findByText('Copy your new key now')).toBeInTheDocument()
    expect(screen.getByDisplayValue('hk_secret-token-value')).toBeInTheDocument()
    const body = JSON.parse(calls.find((c) => c.m === 'POST')!.body!)
    expect(body).toEqual({ name: 'New one', scope: 'read' }) // no expiry field: the server's 90-day default

    await userEvent.click(screen.getByRole('button', { name: 'I’ve copied it' }))
    expect(screen.queryByDisplayValue('hk_secret-token-value')).not.toBeInTheDocument()
  })

  it('offers no expiry as an explicit choice, never the default', async () => {
    const calls = setup()
    await screen.findByText('No API keys yet.')
    expect(screen.getByLabelText('Expires')).toHaveValue('90')
    await userEvent.type(screen.getByLabelText('Name'), 'Terraform')
    await userEvent.selectOptions(screen.getByLabelText('Expires'), 'never')
    expect(screen.getByText(/keeps working until you revoke it/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }))
    await screen.findByText('Copy your new key now')
    expect(JSON.parse(calls.find((c) => c.m === 'POST')!.body!)).toEqual({ name: 'Terraform', scope: 'read', no_expiry: true })
  })

  it('sends a date for the other expiry choices', async () => {
    const calls = setup()
    await screen.findByText('No API keys yet.')
    await userEvent.type(screen.getByLabelText('Name'), 'Monthly')
    await userEvent.selectOptions(screen.getByLabelText('Expires'), '30')
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }))
    await screen.findByText('Copy your new key now')
    const body = JSON.parse(calls.find((c) => c.m === 'POST')!.body!)
    const days = (new Date(body.expires_at).getTime() - Date.now()) / 86_400_000
    expect(days).toBeGreaterThan(29.9)
    expect(days).toBeLessThan(30.1)
  })

  it('offers write access on Free too, and says it only adds monitors there', async () => {
    setup({ write: false })
    await screen.findByText('No API keys yet.')
    expect(screen.getByLabelText('Read and write')).toBeEnabled()
    expect(screen.getByText(/a read and write key can add monitors/)).toBeInTheDocument()
  })

  it('enables write access on Pro', async () => {
    setup({ write: true })
    await screen.findByText('No API keys yet.')
    expect(screen.getByLabelText('Read and write')).toBeEnabled()
  })

  it('asks before revoking', async () => {
    const calls = setup({ keys: [key()] })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    await userEvent.click(await screen.findByRole('button', { name: 'Revoke Grafana' }))
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('stops working right away'))
    expect(calls.some((c) => c.m === 'DELETE' && c.url.endsWith('/api-keys/k1'))).toBe(true)
  })

  it('tells a member the owner can see their keys', async () => {
    setup({ role: 'member', keys: [key()] })
    expect(await screen.findByText(/owner can also see them and revoke them/)).toBeInTheDocument()
    expect(screen.queryByText('Made by')).not.toBeInTheDocument()
  })

  it('needs a verified address to create keys', async () => {
    setup({ verified: false })
    await screen.findByText('No API keys yet.')
    expect(screen.queryByRole('button', { name: 'Create key' })).not.toBeInTheDocument()
    expect(screen.getByText(/Verify your email address first/)).toBeInTheDocument()
  })

  it('says when keys are turned off on the server', async () => {
    setup({ disabled: true })
    expect(await screen.findByText(/API keys are turned off on this server/)).toBeInTheDocument()
  })
})
