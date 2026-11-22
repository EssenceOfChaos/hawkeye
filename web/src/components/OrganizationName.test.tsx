import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../AuthProvider'
import { OrganizationName } from './OrganizationName'

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

/** A signed-in person in one organization whose name the server keeps, so a rename shows in the next /auth/me. */
function setup(role: 'owner' | 'member', initial = "freddy's organization", patch?: () => Promise<Response>) {
  document.cookie = 'csrf_token=t; path=/'
  let name = initial
  const patches: unknown[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) {
      return json(200, { user: { id: 'u', email: 'freddy@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'org-1', name, role }] })
    }
    if (init?.method === 'PATCH' && url.endsWith('/organizations/org-1')) {
      const body = JSON.parse(init.body as string)
      patches.push(body)
      if (patch) return patch()
      name = body.name
      return json(200, { organization: { id: 'org-1', name, role } })
    }
    return json(404, {})
  }))
  render(<MemoryRouter><AuthProvider><OrganizationName /></AuthProvider></MemoryRouter>)
  return patches
}

describe('OrganizationName', () => {
  it('lets the owner rename the organization, nudging away from the name made at sign-up', async () => {
    const patches = setup('owner')
    const field = await screen.findByLabelText('Name')
    expect(field).toHaveValue("freddy's organization")
    expect(screen.getByText(/Name it after your company or team/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save name' })).toBeDisabled() // nothing changed yet

    await userEvent.clear(field)
    await userEvent.type(field, 'Acme Agency')
    await userEvent.click(screen.getByRole('button', { name: 'Save name' }))
    expect(await screen.findByText('Saved.')).toBeInTheDocument()
    expect(patches).toEqual([{ name: 'Acme Agency' }])
    // The app reloaded the organization: the nudge is gone and the button is disabled again.
    await waitFor(() => expect(screen.queryByText(/Name it after your company or team/)).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Save name' })).toBeDisabled()
  })

  it('shows the server’s reason when a name is refused', async () => {
    setup('owner', 'Acme', () => json(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { name: 'must be between 1 and 100 characters' } } } }))
    const field = await screen.findByLabelText('Name')
    await userEvent.type(field, ' Labs')
    await userEvent.click(screen.getByRole('button', { name: 'Save name' }))
    expect(await screen.findByText('must be between 1 and 100 characters')).toBeInTheDocument()
    expect(field).toHaveAttribute('aria-invalid', 'true')
  })

  it('shows members the name without a way to change it', async () => {
    setup('member', 'Acme Agency')
    expect(await screen.findByText('Acme Agency')).toBeInTheDocument()
    expect(screen.getByText(/Its owner can change the name/)).toBeInTheDocument()
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument()
  })
})
