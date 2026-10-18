import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AppRoutes } from './App'
import { AuthProvider } from './auth'

afterEach(() => vi.unstubAllGlobals())

function jsonResponse(status: number, body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <AppRoutes />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('routing', () => {
  it('shows the 404 page for an unknown address instead of redirecting a visitor who is not signed in', async () => {
    vi.stubGlobal('fetch', vi.fn(() => jsonResponse(401, { error: { code: 'UNAUTHENTICATED', message: 'Authentication required.' } })))
    renderAt('/no-such-page')
    expect(await screen.findByRole('heading', { level: 1, name: /Beyond our field of view/ })).toBeInTheDocument()
  })

  it('shows the 404 page for an unknown address to signed-in users too', async () => {
    const user = { id: 'u', email: 'a@example.com', email_verified_at: '2026-01-01T00:00:00Z' }
    vi.stubGlobal('fetch', vi.fn(() => jsonResponse(200, { user, organizations: [] })))
    renderAt('/monitors/1/unknown/extra')
    expect(await screen.findByRole('heading', { level: 1, name: /Beyond our field of view/ })).toBeInTheDocument()
  })

  it('sends anonymous visitors to the login page', async () => {
    vi.stubGlobal('fetch', vi.fn(() => jsonResponse(401, { error: { code: 'UNAUTHENTICATED', message: 'Authentication required.' } })))
    renderAt('/monitors')
    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument()
  })

  it('shows the monitors page to signed-in users', async () => {
    const user = { id: 'u', email: 'a@example.com', email_verified_at: '2026-01-01T00:00:00Z' }
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url.includes('/auth/me')) return jsonResponse(200, { user, organizations: [] })
        if (url.includes('/usage')) return jsonResponse(200, { usage: { plan: { name: 'Free', max_monitors: 5 }, monitors_used: 0, monitor_limit: 5, over_limit: false, next_plan: null } })
        return jsonResponse(200, { monitors: [], total: 0 })
      }),
    )
    renderAt('/monitors')
    expect(await screen.findByRole('heading', { name: 'Add your first website' })).toBeInTheDocument()
    expect(screen.getByText(/of/)).toBeInTheDocument()
  })
})
