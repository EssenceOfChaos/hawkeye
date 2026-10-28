import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import { AuthProvider, useAuth } from './auth'

afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
  window.localStorage.clear()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

// A team that is OLDER than the person's own organization, which is how it looks for anyone invited to an established team.
const TEAM = { id: 'team', name: 'Acme', role: 'member' }
const OWN = { id: 'own', name: 'Mine', role: 'owner' }

function Probe() {
  const { organization, organizations, selectOrganization } = useAuth()
  return (
    <>
      <output data-testid="active">{organization ? `${organization.name}:${organization.role}` : 'none'}</output>
      {organizations.map((o) => (
        <button key={o.id} onClick={() => selectOrganization(o.id)}>use {o.name}</button>
      ))}
    </>
  )
}

function mount(orgs: unknown[]) {
  const seen: { url: string; org: string | null }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      seen.push({ url, org: (init?.headers as Record<string, string> | undefined)?.['X-Organization-ID'] ?? null })
      if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'u@example.com', email_verified_at: null }, organizations: orgs })
      return json(200, {})
    }),
  )
  render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  )
  return seen
}

describe('the organization the app acts for', () => {
  it('defaults to the one the person owns, even when a team they belong to is older, and names no organization to the server', async () => {
    const seen = mount([TEAM, OWN])
    expect(await screen.findByText('Mine:owner')).toBeInTheDocument()
    await api('GET', '/monitors')
    expect(seen.find((s) => s.url.endsWith('/monitors'))?.org).toBeNull()
  })

  it('switches to the team, tells the server which organization every request is for, and remembers the choice', async () => {
    const seen = mount([TEAM, OWN])
    await userEvent.click(await screen.findByRole('button', { name: 'use Acme' }))
    expect(screen.getByTestId('active')).toHaveTextContent('Acme:member')
    await api('GET', '/monitors')
    expect(seen.find((s) => s.url.endsWith('/monitors'))?.org).toBe('team')
    expect(window.localStorage.getItem('hawkeye_organization')).toBe('team')

    await userEvent.click(screen.getByRole('button', { name: 'use Mine' }))
    await api('GET', '/incidents')
    expect(seen.find((s) => s.url.endsWith('/incidents'))?.org).toBeNull()
  })

  it('goes back to the default when the remembered organization is no longer one the person belongs to', async () => {
    window.localStorage.setItem('hawkeye_organization', 'removed-team')
    const seen = mount([OWN])
    expect(await screen.findByText('Mine:owner')).toBeInTheDocument()
    await api('GET', '/monitors')
    expect(seen.find((s) => s.url.endsWith('/monitors'))?.org).toBeNull()
  })

  it('reloads and falls back when the server says the organization is gone (the person was removed)', async () => {
    window.localStorage.setItem('hawkeye_organization', 'team')
    let removed = false
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'u@example.com', email_verified_at: null }, organizations: removed ? [OWN] : [TEAM, OWN] })
        removed = true
        return json(404, { error: { code: 'NOT_FOUND', message: 'Organization not found.' } })
      }),
    )
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    )
    expect(await screen.findByText('Acme:member')).toBeInTheDocument()
    await expect(api('GET', '/monitors')).rejects.toThrow()
    expect(await screen.findByText('Mine:owner')).toBeInTheDocument()
    expect(window.localStorage.getItem('hawkeye_organization')).toBeNull()
  })
})
