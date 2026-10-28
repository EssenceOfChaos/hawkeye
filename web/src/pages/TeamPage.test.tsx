import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { TeamPage } from './TeamPage'

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
const noContent = () => Promise.resolve(new Response(null, { status: 204 }))

const OWNER = { user_id: 'u-owner', email: 'owner@example.com', role: 'owner', created_at: '2026-10-01T00:00:00Z' }
const BOB = { user_id: 'u-bob', email: 'bob@example.com', role: 'member', created_at: '2026-10-10T00:00:00Z' }
const PENDING = { id: 'inv-1', email: 'carol@example.com', created_at: '2026-10-20T00:00:00Z', expires_at: '2026-10-27T00:00:00Z' }

interface Opts {
  me?: { id: string; email: string; verified?: boolean }
  role?: 'owner' | 'member'
  extraOrg?: boolean
  seats?: number
  planName?: string
  members?: unknown[]
  invitations?: unknown[]
  onPost?: (url: string) => Promise<Response> | undefined
}

function setup(o: Opts = {}) {
  document.cookie = 'csrf_token=t; path=/'
  // A member of a team also has their own organization, which is what the app opens by default; viewing the team is a choice.
  if (o.role === 'member') window.localStorage.setItem('hawkeye_organization', 'org-1')
  const calls: { m: string; url: string; body?: unknown }[] = []
  const me = o.me ?? { id: 'u-owner', email: 'owner@example.com', verified: true }
  const orgs = [{ id: 'org-1', name: 'Acme', role: o.role ?? 'owner' }, ...(o.extraOrg ? [{ id: 'org-2', name: 'Mine', role: 'owner' }] : [])]
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      const m = init?.method ?? 'GET'
      calls.push({ m, url, body: init?.body ? JSON.parse(init.body as string) : undefined })
      if (url.includes('/auth/me')) {
        return json(200, { user: { id: me.id, email: me.email, email_verified_at: me.verified === false ? null : '2026-01-01T00:00:00Z' }, organizations: orgs })
      }
      if (m === 'POST' && o.onPost) {
        const r = o.onPost(url)
        if (r) return r
      }
      if (m === 'POST' && url.endsWith('/invitations')) return json(201, { invitation: PENDING })
      if (m === 'POST' || m === 'DELETE') return noContent()
      if (url.endsWith('/members')) return json(200, { members: o.members ?? [OWNER, BOB] })
      if (url.endsWith('/invitations')) return json(200, { invitations: o.invitations ?? [PENDING] })
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { code: 'agency', name: o.planName ?? 'Agency', max_team_members: o.seats ?? 5 }, monitors_used: 0, monitor_limit: 200, over_limit: false, next_plan: null } })
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <TeamPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

describe('TeamPage for the owner', () => {
  it('lists the team and the invitations still waiting, and counts both against the seats', async () => {
    setup()
    expect(await screen.findByText('bob@example.com')).toBeInTheDocument()
    expect(screen.getByText('owner@example.com')).toBeInTheDocument()
    expect(screen.getByText('(you)')).toBeInTheDocument()
    expect(screen.getByText('Owner')).toBeInTheDocument()
    expect(screen.getByText('carol@example.com')).toBeInTheDocument()
    expect(screen.getByText(/3 of 5 seats used on the Agency plan/)).toBeInTheDocument()
    expect(screen.getByText(/invitation holds a seat/)).toBeInTheDocument()
  })

  it('sends an invitation and reloads the list', async () => {
    const calls = setup({ invitations: [] })
    await userEvent.type(await screen.findByLabelText('Email address'), ' Carol@Example.com ')
    await userEvent.click(screen.getByRole('button', { name: 'Send invitation' }))
    expect(await screen.findByText('Invitation sent to carol@example.com.')).toBeInTheDocument()
    const post = calls.find((c) => c.m === 'POST' && c.url.endsWith('/organizations/org-1/invitations'))
    expect(post?.body).toEqual({ email: 'Carol@Example.com' }) // the server normalises the case
    expect(calls.filter((c) => c.m === 'GET' && c.url.endsWith('/invitations')).length).toBeGreaterThanOrEqual(2)
  })

  it('shows the server’s reason in full when the plan has no seat left, with a way to upgrade', async () => {
    setup({
      invitations: [],
      onPost: (url) =>
        url.endsWith('/invitations')
          ? json(403, { error: { code: 'PLAN_LIMIT_REACHED', message: 'Your Agency plan includes up to 5 team members, counting people you have invited who have not joined yet.' } })
          : undefined,
    })
    await userEvent.type(await screen.findByLabelText('Email address'), 'dan@example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Send invitation' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/includes up to 5 team members/)
    expect(screen.getByRole('link', { name: 'See plans' })).toHaveAttribute('href', '/billing')
  })

  it('turns the form off when every seat is taken', async () => {
    setup({ seats: 3 })
    expect(await screen.findByText('bob@example.com')).toBeInTheDocument()
    expect(screen.getByLabelText('Email address')).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Send invitation' })).toBeDisabled()
    expect(screen.getByText(/All 3 seats are taken/)).toBeInTheDocument()
  })

  it('removes a member only after confirming, and never offers to remove the owner', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const calls = setup()
    await screen.findByText('bob@example.com')
    expect(screen.queryByRole('button', { name: 'Remove owner@example.com' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Remove bob@example.com' }))
    expect(calls.some((c) => c.m === 'DELETE')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Remove bob@example.com' }))
    expect(await screen.findByText('bob@example.com was removed.')).toBeInTheDocument()
    expect(calls.find((c) => c.m === 'DELETE')?.url).toBe('/api/v1/organizations/org-1/members/u-bob')
    confirm.mockRestore()
  })

  it('resends and cancels a pending invitation', async () => {
    const calls = setup()
    const row = (await screen.findByText('carol@example.com')).closest('tr')!
    await userEvent.click(within(row).getByRole('button', { name: 'Resend' }))
    expect(await screen.findByText('A new invitation was sent to carol@example.com.')).toBeInTheDocument()
    expect(calls.some((c) => c.m === 'POST' && c.url.endsWith('/invitations/inv-1/resend'))).toBe(true)
    await userEvent.click(within(row).getByRole('button', { name: 'Cancel the invitation to carol@example.com' }))
    expect(await screen.findByText('The invitation to carol@example.com was cancelled.')).toBeInTheDocument()
    expect(calls.some((c) => c.m === 'DELETE' && c.url.endsWith('/invitations/inv-1'))).toBe(true)
  })

  it('explains that a one-person plan cannot invite, instead of showing a form that cannot work', async () => {
    setup({ seats: 1, planName: 'Pro', members: [OWNER], invitations: [] })
    expect(await screen.findByText(/Your Pro plan is for one person/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Upgrade' })).toHaveAttribute('href', '/billing')
    expect(screen.queryByLabelText('Email address')).not.toBeInTheDocument()
  })

  it('asks the owner to verify their own address first', async () => {
    setup({ me: { id: 'u-owner', email: 'owner@example.com', verified: false } })
    expect(await screen.findByText(/Verify your own email address first/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send invitation' })).toBeDisabled()
  })
})

describe('TeamPage for a member', () => {
  it('shows the team read-only and never asks for the owner’s invitations', async () => {
    const calls = setup({ role: 'member', me: { id: 'u-bob', email: 'bob@example.com' }, extraOrg: true, invitations: [PENDING] })
    expect(await screen.findByText('owner@example.com')).toBeInTheDocument()
    expect(screen.queryByText('carol@example.com')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Remove/ })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Email address')).not.toBeInTheDocument()
    expect(screen.getByText(/Only its owner can invite or remove people/)).toBeInTheDocument()
    expect(calls.some((c) => c.url.endsWith('/invitations'))).toBe(false)
  })

  it('lets a member leave after confirming', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const calls = setup({ role: 'member', me: { id: 'u-bob', email: 'bob@example.com' }, extraOrg: true })
    await userEvent.click(await screen.findByRole('button', { name: 'Leave Acme' }))
    await vi.waitFor(() => expect(calls.some((c) => c.m === 'POST' && c.url === '/api/v1/organizations/org-1/leave')).toBe(true))
  })
})
