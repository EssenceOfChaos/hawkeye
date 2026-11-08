import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { ConnectPage, leave } from './ConnectPage'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const query = '?response_type=code&client_id=hkc_1&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback&state=s&code_challenge=c&code_challenge_method=S256&scope=read+write'

const orgs = [
  { id: 'o1', name: 'Acme', role: 'owner', can_write: true, blocked: false },
  { id: 'o2', name: 'Side project', role: 'owner', can_write: false, blocked: false },
  { id: 'o3', name: 'Client team', role: 'member', can_write: true, blocked: true },
]

function setup(info: Record<string, unknown>, approve: () => Promise<Response> = () => json(200, { redirect: 'https://claude.ai/api/mcp/auth_callback?code=abc&state=s' })) {
  const calls: { method: string; url: string; body?: any }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body ? JSON.parse(init.body as string) : undefined })
    if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [] })
    if (url.includes('/oauth/authorize') && method === 'GET') return json(200, info)
    if (url.includes('/oauth/authorize') && method === 'POST') return approve()
    return json(404, {})
  }))
  const went = vi.spyOn(leave, 'to').mockImplementation(() => undefined)
  render(
    <MemoryRouter initialEntries={['/connect' + query]}>
      <AuthProvider>
        <Routes><Route path="/connect" element={<ConnectPage />} /></Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
  return { calls, went }
}

const base = { client: { name: 'Claude' }, requests_write: true, organizations: orgs, two_factor_code_needed: false,
  deny_redirect: 'https://claude.ai/api/mcp/auth_callback?error=access_denied&state=s', redirect_host: 'claude.ai', redirect_loopback: false }

describe('ConnectPage', () => {
  it('names the app, lets the person choose, and sends them back with the code', async () => {
    const { calls, went } = setup(base)
    expect(await screen.findByRole('heading', { name: 'Connect Claude to HawkEye' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Client team.*needs two-factor/ })).toBeDisabled()
    await userEvent.click(screen.getByLabelText(/Read and make changes/))
    await userEvent.click(screen.getByRole('button', { name: 'Allow' }))
    const post = calls.find((c) => c.method === 'POST')!
    expect(post.body).toMatchObject({ organization_id: 'o1', access: 'write', client_id: 'hkc_1', code_challenge: 'c', state: 's' })
    expect(went).toHaveBeenCalledWith('https://claude.ai/api/mcp/auth_callback?code=abc&state=s')
  })

  it('shows where the person goes back to, and warns about a local address', async () => {
    setup({ ...base, redirect_host: 'localhost', redirect_loopback: true })
    expect(await screen.findByText('localhost', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/runs on your own computer/)).toBeInTheDocument()
  })

  it('offers only read access when the plan can’t make changes', async () => {
    const { calls } = setup(base)
    await userEvent.selectOptions(await screen.findByLabelText('Organization'), 'o2')
    expect(screen.getByLabelText(/Read and make changes/)).toBeDisabled()
    expect(screen.getByText(/can read through the API but not make changes/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Allow' }))
    expect(calls.find((c) => c.method === 'POST')!.body).toMatchObject({ organization_id: 'o2', access: 'read' })
  })

  it('asks for a two-factor code when the person uses it, and shows a refusal', async () => {
    const { calls } = setup({ ...base, two_factor_code_needed: true }, () => json(400, { error: { code: 'INVALID_CODE', message: 'That code is not correct.' } }))
    await userEvent.type(await screen.findByLabelText('Code from your authenticator app'), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Allow' }))
    expect(await screen.findByText('That code is not correct.')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'POST')!.body.two_factor_code).toBe('123456')
  })

  it('sends a denial back to the app', async () => {
    const { went } = setup(base)
    await userEvent.click(await screen.findByRole('button', { name: 'Deny' }))
    expect(went).toHaveBeenCalledWith(base.deny_redirect)
  })

  it('passes a bad request straight back to the app', async () => {
    const { went } = setup({ error_redirect: 'https://claude.ai/api/mcp/auth_callback?error=invalid_request' })
    await vi.waitFor(() => expect(went).toHaveBeenCalledWith('https://claude.ai/api/mcp/auth_callback?error=invalid_request'))
  })
})
