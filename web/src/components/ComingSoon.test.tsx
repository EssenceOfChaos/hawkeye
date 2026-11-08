import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { WaitlistConfirmPage, WaitlistLeavePage } from '../pages/AuthPages'
import { COMING_SOON, ComingSoon } from './ComingSoon'

afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function stub(opts: { user?: 'verified' | 'unverified'; reply?: () => Promise<Response> } = {}) {
  const posts: { url: string; body: any }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) {
      return opts.user
        ? json(200, { user: { id: 'u', email: 'me@example.org', email_verified_at: opts.user === 'verified' ? '2026-01-01T00:00:00Z' : null }, organizations: [] })
        : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
    }
    if (init?.method === 'POST') {
      posts.push({ url, body: init.body ? JSON.parse(init.body as string) : undefined })
      return opts.reply ? opts.reply() : json(202, { status: 'check_email' })
    }
    return json(404, {})
  }))
  return posts
}

function mountSection() {
  render(
    <MemoryRouter>
      <AuthProvider>
        <ComingSoon />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('Coming soon', () => {
  it('shows every coming-soon feature with its own "Get notified" button', async () => {
    stub()
    mountSection()
    for (const f of COMING_SOON) {
      expect(screen.getByRole('heading', { name: f.title })).toBeInTheDocument()
      expect(await screen.findByRole('button', { name: `Get notified about ${f.title}` })).toBeInTheDocument()
    }
  })

  it('asks a signed-out visitor for an address, then to confirm it from their inbox', async () => {
    const posts = stub()
    mountSection()
    await userEvent.click(await screen.findByRole('button', { name: 'Get notified about Multi-step API checks' }))
    await userEvent.type(screen.getByLabelText('Your email address'), 'visitor@example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Notify me' }))
    expect(await screen.findByText(/Check your inbox/)).toBeInTheDocument()
    expect(posts).toEqual([{ url: expect.stringContaining('/waitlist'), body: { feature: 'api_workflows', email: 'visitor@example.com', company: '' } }])
  })

  it('adds a verified, signed-in person in one click', async () => {
    const posts = stub({ user: 'verified', reply: () => json(200, { status: 'added' }) })
    mountSection()
    await userEvent.click(await screen.findByRole('button', { name: 'Get notified about Status pages on your own domain' }))
    expect(await screen.findByText(/You’re on the list\. We’ll email me@example\.org/)).toBeInTheDocument()
    expect(posts[0].body).toEqual({ feature: 'status_page_domains', company: '' })
    expect(screen.queryByLabelText('Your email address')).not.toBeInTheDocument()
  })

  it('asks an unverified, signed-in person for an address like anyone else', async () => {
    stub({ user: 'unverified' })
    mountSection()
    await userEvent.click(await screen.findByRole('button', { name: 'Get notified about Email updates for status page visitors' }))
    expect(screen.getByLabelText('Your email address')).toBeInTheDocument()
  })

  it('shows the error when signing up fails', async () => {
    stub({ reply: () => json(400, { error: { code: 'VALIDATION_ERROR', message: 'Enter a valid email address.' } }) })
    mountSection()
    await userEvent.click(await screen.findByRole('button', { name: 'Get notified about Multi-step API checks' }))
    await userEvent.type(screen.getByLabelText('Your email address'), 'a@b.co')
    await userEvent.click(screen.getByRole('button', { name: 'Notify me' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Enter a valid email address.')
  })
})

function mountPage(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <Routes>
          <Route path="/waitlist/confirm" element={<WaitlistConfirmPage />} />
          <Route path="/waitlist/leave" element={<WaitlistLeavePage />} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('waitlist links', () => {
  it('confirms only when the button is pressed, never just by opening the link', async () => {
    const posts = stub({ reply: () => json(200, { feature: 'multi-step API checks' }) })
    mountPage('/waitlist/confirm?token=TOKEN1')
    const button = await screen.findByRole('button', { name: 'Confirm' })
    expect(posts).toEqual([])
    await userEvent.click(button)
    expect(await screen.findByText(/You’re on the list for multi-step API checks/)).toBeInTheDocument()
    expect(posts).toEqual([{ url: expect.stringContaining('/waitlist/confirm'), body: { token: 'TOKEN1' } }])
  })

  it('says when a confirmation link has expired', async () => {
    stub({ reply: () => json(400, { error: { code: 'INVALID_TOKEN', message: 'x' } }) })
    mountPage('/waitlist/confirm?token=OLD')
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid or has expired')
  })

  it('removes an address only when the button is pressed', async () => {
    const posts = stub({ reply: () => Promise.resolve(new Response(null, { status: 204 })) })
    mountPage('/waitlist/leave?token=00000000-0000-0000-0000-000000000001')
    const button = await screen.findByRole('button', { name: 'Take me off the list' })
    expect(posts).toEqual([])
    await userEvent.click(button)
    expect(await screen.findByText(/This address is off the list/)).toBeInTheDocument()
    expect(posts[0].body).toEqual({ token: '00000000-0000-0000-0000-000000000001' })
  })
})
