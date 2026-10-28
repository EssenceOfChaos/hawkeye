import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { AcceptInvitePage } from './AuthPages'

afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
  window.localStorage.clear()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.pathname + l.search}</output>
}

const PREVIEW = { invitation: { organization_name: 'Acme', email: 'bob@example.com' } }

function mount(o: { signedInAs?: { email: string; verified?: boolean }; preview?: Promise<Response>; accept?: Promise<Response>; token?: string }) {
  const calls: { m: string; url: string; body?: any }[] = []
  let signedIn = Boolean(o.signedInAs)
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      const m = init?.method ?? 'GET'
      calls.push({ m, url, body: init?.body ? JSON.parse(init.body as string) : undefined })
      if (url.includes('/auth/me')) {
        if (!signedIn) return json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
        const orgs = [{ id: 'own', name: 'Mine', role: 'owner' }, ...(calls.some((c) => c.url.endsWith('/invitations/accept')) ? [{ id: 'team', name: 'Acme', role: 'member' }] : [])]
        return json(200, { user: { id: 'u', email: o.signedInAs!.email, email_verified_at: o.signedInAs!.verified === false ? null : '2026-01-01T00:00:00Z' }, organizations: orgs })
      }
      if (url.endsWith('/auth/logout')) {
        signedIn = false
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      if (url.endsWith('/invitations/preview')) return o.preview ?? json(200, PREVIEW)
      if (url.endsWith('/invitations/accept')) return o.accept ?? json(200, { organization: { id: 'team', name: 'Acme', role: 'member' } })
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter initialEntries={[`/accept-invite?token=${o.token ?? 'INVITE-1'}`]}>
      <AuthProvider>
        <Where />
        <Routes>
          <Route path="/accept-invite" element={<AcceptInvitePage />} />
          <Route path="/dashboard" element={<p>dashboard</p>} />
          <Route path="/login" element={<p>login page</p>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('AcceptInvitePage', () => {
  it('says who the invitation is for, keeps the token out of the address bar, and offers to log in or sign up', async () => {
    const calls = mount({})
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(screen.getAllByText('bob@example.com').length).toBeGreaterThan(0)
    expect(screen.getByTestId('where')).not.toHaveTextContent('INVITE-1')
    expect(calls.find((c) => c.url.endsWith('/invitations/preview'))?.body).toEqual({ token: 'INVITE-1' })
    expect(screen.getByRole('link', { name: 'Log in' })).toHaveAttribute('href', '/login')
    expect(screen.getByRole('link', { name: 'Create account' })).toHaveAttribute('href', '/register')
    // Nothing has been accepted by merely opening the link.
    expect(calls.some((c) => c.url.endsWith('/invitations/accept'))).toBe(false)
  })

  it('joins the team with the signed-in account, switches to it and lands on the dashboard', async () => {
    const calls = mount({ signedInAs: { email: 'Bob@example.com' } })
    await userEvent.click(await screen.findByRole('button', { name: 'Accept invitation' }))
    expect(await screen.findByText('dashboard')).toBeInTheDocument()
    expect(calls.find((c) => c.url.endsWith('/invitations/accept'))?.body).toEqual({ token: 'INVITE-1' })
    expect(window.localStorage.getItem('hawkeye_organization')).toBe('team')
  })

  it('does not offer to accept as the wrong account, and lets the person log out', async () => {
    mount({ signedInAs: { email: 'someone-else@example.com' } })
    expect(await screen.findByText(/signed in as someone-else@example.com, but this invitation is for bob@example.com/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Accept invitation' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Log out and use bob@example.com' }))
    expect(await screen.findByRole('link', { name: 'Log in' })).toBeInTheDocument()
  })

  it('shows the server’s message when the address is not verified yet', async () => {
    mount({
      signedInAs: { email: 'bob@example.com', verified: false },
      accept: json(403, { error: { code: 'EMAIL_NOT_VERIFIED', message: 'Verify your email address to accept this invitation.' } }),
    })
    expect(await screen.findByText(/Confirm your email address first/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Accept invitation' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Verify your email address to accept this invitation.')
    expect(screen.getByRole('button', { name: 'Accept invitation' })).toBeEnabled() // they can try again after verifying
  })

  it('says so when the link is no good', async () => {
    mount({ preview: json(400, { error: { code: 'INVALID_TOKEN', message: 'This invitation is invalid or has expired.' } }) })
    expect(await screen.findByRole('alert')).toHaveTextContent(/not valid, or it has expired/)
    expect(screen.queryByRole('link', { name: 'Log in' })).not.toBeInTheDocument()
  })

  it('says so when there is no token at all', async () => {
    mount({ token: '' })
    expect(await screen.findByRole('alert')).toHaveTextContent(/not valid, or it has expired/)
  })
})
