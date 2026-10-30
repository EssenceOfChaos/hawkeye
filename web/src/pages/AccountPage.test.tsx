import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { AccountPage } from './AccountPage'
import { PrivacyPage, TermsPage } from './LegalPages'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

interface Setup {
  verified?: boolean
  post?: (url: string, body: any) => Promise<Response>
  exportResponse?: () => Promise<Response>
}

function setup(o: Setup = {}) {
  const posts: { url: string; body: any }[] = []
  let signedIn = true
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) {
      return signedIn
        ? json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: o.verified === false ? null : '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: 'owner' }] })
        : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
    }
    if (url.includes('/account/export')) return o.exportResponse ? o.exportResponse() : Promise.resolve(new Response('{"user":{}}', { status: 200 }))
    if (init?.method === 'POST') {
      const body = init.body ? JSON.parse(init.body as string) : undefined
      posts.push({ url, body })
      if (url.endsWith('/account/delete')) signedIn = false
      return o.post ? o.post(url, body) : Promise.resolve(new Response(null, { status: 204 }))
    }
    return json(404, {})
  }))
  render(
    <MemoryRouter initialEntries={['/account']}>
      <AuthProvider>
        <Routes>
          <Route path="/account" element={<AccountPage />} />
          <Route path="/" element={<h1>Home page</h1>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
  return posts
}

describe('AccountPage', () => {
  it('shows the email and verification state', async () => {
    setup()
    expect(await screen.findByText('me@example.com')).toBeInTheDocument()
    expect(screen.getByText('Email verified')).toBeInTheDocument()
  })

  it('offers to resend verification when the email is unverified', async () => {
    const posts = setup({ verified: false })
    await userEvent.click(await screen.findByRole('button', { name: 'Resend verification email' }))
    expect(posts[0].url).toBe('/api/v1/auth/resend-verification')
    expect(await screen.findByText(/new link is on its way/)).toBeInTheDocument()
  })

  it('checks the two new passwords match before calling the server', async () => {
    const posts = setup()
    await userEvent.type(await screen.findByLabelText('Current password'), 'old-password-1')
    await userEvent.type(screen.getByLabelText('New password'), 'a-brand-new-password')
    await userEvent.type(screen.getByLabelText('Repeat new password'), 'a-different-password')
    await userEvent.click(screen.getByRole('button', { name: 'Change password' }))
    expect(await screen.findByText('The two new passwords do not match.')).toBeInTheDocument()
    expect(posts).toHaveLength(0)
  })

  it('changes the password and says other devices were signed out', async () => {
    const posts = setup()
    await userEvent.type(await screen.findByLabelText('Current password'), 'old-password-1')
    await userEvent.type(screen.getByLabelText('New password'), 'a-brand-new-password')
    await userEvent.type(screen.getByLabelText('Repeat new password'), 'a-brand-new-password')
    await userEvent.click(screen.getByRole('button', { name: 'Change password' }))
    expect(await screen.findByText(/Other devices were signed out/)).toBeInTheDocument()
    expect(posts[0]).toEqual({ url: '/api/v1/auth/change-password', body: { current_password: 'old-password-1', new_password: 'a-brand-new-password' } })
  })

  it('shows a wrong current password next to that field', async () => {
    setup({ post: () => json(403, { error: { code: 'WRONG_PASSWORD', message: 'That password is not correct.' } }) })
    await userEvent.type(await screen.findByLabelText('Current password'), 'nope-nope-nope')
    await userEvent.type(screen.getByLabelText('New password'), 'a-brand-new-password')
    await userEvent.type(screen.getByLabelText('Repeat new password'), 'a-brand-new-password')
    await userEvent.click(screen.getByRole('button', { name: 'Change password' }))
    expect(await screen.findByText('That is not your current password.')).toBeInTheDocument()
    expect(screen.getByLabelText('Current password')).toHaveAttribute('aria-invalid', 'true')
  })

  it('downloads the export as a file', async () => {
    const created = vi.fn(() => 'blob:fake')
    const revoked = vi.fn()
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: created, revokeObjectURL: revoked }))
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    setup()
    await userEvent.click(await screen.findByRole('button', { name: 'Download my data' }))
    await waitFor(() => expect(click).toHaveBeenCalled())
    expect(created).toHaveBeenCalled()
    expect(revoked).toHaveBeenCalledWith('blob:fake')
  })

  it('reports an export failure', async () => {
    setup({ exportResponse: () => json(429, { error: { code: 'RATE_LIMITED', message: 'Too many requests. Try again later.' } }) })
    await userEvent.click(await screen.findByRole('button', { name: 'Download my data' }))
    expect(await screen.findByText('Too many requests. Try again later.')).toBeInTheDocument()
  })

  it('keeps deletion disabled until the email is typed exactly and a password is given', async () => {
    setup()
    const btn = await screen.findByRole('button', { name: 'Delete my account' })
    expect(btn).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Your password'), 'my-password-123')
    await userEvent.type(screen.getByLabelText('Type me@example.com to confirm'), 'me@exampl')
    expect(btn).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Type me@example.com to confirm'), 'e.com')
    expect(btn).toBeEnabled()
  })

  it('asks for a final confirmation, deletes, and returns to the home page', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const posts = setup()
    await userEvent.type(await screen.findByLabelText('Your password'), 'my-password-123')
    await userEvent.type(screen.getByLabelText('Type me@example.com to confirm'), 'me@example.com')
    const btn = screen.getByRole('button', { name: 'Delete my account' })
    await userEvent.click(btn)
    expect(posts).toHaveLength(0) // cancelled at the confirm dialog
    await userEvent.click(btn)
    expect(confirm).toHaveBeenCalledTimes(2)
    expect(posts[0]).toEqual({ url: '/api/v1/account/delete', body: { password: 'my-password-123', confirm_email: 'me@example.com' } })
    expect(await screen.findByRole('heading', { name: 'Home page' })).toBeInTheDocument()
  })

  it('explains why a shared organization blocks deletion', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    setup({ post: () => json(409, { error: { code: 'OWNS_SHARED_ORGANIZATION', message: 'You own an organization that has other members. Remove them first.' } }) })
    await userEvent.type(await screen.findByLabelText('Your password'), 'my-password-123')
    await userEvent.type(screen.getByLabelText('Type me@example.com to confirm'), 'me@example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Delete my account' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/other members/)
    expect(screen.queryByRole('heading', { name: 'Home page' })).not.toBeInTheDocument()
  })

  it('warns that a paid subscription is cancelled immediately without refund', async () => {
    setup()
    expect(await screen.findByText(/cancelled immediately and is not refunded/)).toBeInTheDocument()
  })
})

describe('legal pages', () => {
  it('the privacy policy is complete (no unfilled placeholders) and states the real retention periods and contact', () => {
    const { container } = render(<MemoryRouter><PrivacyPage /></MemoryRouter>)
    expect(screen.getByRole('heading', { level: 1, name: 'HawkEye Privacy Policy' })).toBeInTheDocument()
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
    const text = container.textContent ?? ''
    expect(text).not.toMatch(/\[(INSERT|CONFIRM|IDENTIFY|LIST|ATTORNEY)/i)
    expect(text).not.toMatch(/PLACEHOLDER/)
    for (const days of ['7 days', '90 days', '365 days']) expect(text).toContain(days)
    expect(screen.getAllByRole('link', { name: 'support@hawkeye.observer' })[0]).toHaveAttribute('href', 'mailto:support@hawkeye.observer')
    expect(screen.getAllByRole('link', { name: 'Terms of service' }).length).toBeGreaterThan(0)
  })

  it('the privacy policy names every service provider and cookie the app really uses', () => {
    const { container } = render(<MemoryRouter><PrivacyPage /></MemoryRouter>)
    const text = container.textContent ?? ''
    for (const word of ['Vultr', 'Stripe', 'Resend', 'Cloudflare', 'Google Analytics', 'Google Ads', '_ga', '_gcl_au', 'hawkeye_session', 'csrf_token', 'at least 18']) expect(text).toContain(word)
  })

  it('terms are complete, require authorization to monitor and disclaim guaranteed detection', () => {
    const { container } = render(<MemoryRouter><TermsPage /></MemoryRouter>)
    const text = container.textContent ?? ''
    expect(screen.getByRole('heading', { level: 1, name: 'HawkEye Terms and Conditions' })).toBeInTheDocument()
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
    expect(text).not.toMatch(/\[(INSERT|CONFIRM|IDENTIFY|LIST|ATTORNEY)/i)
    expect(text).not.toMatch(/PLACEHOLDER|Attorney review/i)
    expect(text).toMatch(/own or are authorized to test or monitor/)
    expect(text).toMatch(/best-effort basis/)
    expect(text).toMatch(/cannot guarantee that every outage/)
    expect(text).toContain('at least 18')
    expect(text).toContain('Commonwealth of Pennsylvania')
  })

  it('terms only promise what the product does today', () => {
    const { container } = render(<MemoryRouter><TermsPage /></MemoryRouter>)
    const text = container.textContent ?? ''
    // Plan limits mirror the seeded plans; unbuilt features must not be promised.
    for (const want of ['up to 5 monitors', 'up to 50 monitors', 'up to 200 monitors', '$19 per month', '$59 per month', 'SMS is not currently available', 'Maintenance windows are included in the Pro and Agency plans']) expect(text).toContain(want)
    // Team seats are built: only the Agency plan has them, and the Terms say who is responsible for invited people.
    for (const want of ['up to 5 team members (you and up to four invited colleagues)', 'You are responsible for whom you invite', 'you can remove anyone at any time']) expect(text).toContain(want)
    expect(text.match(/team members/g)).toHaveLength(1)
    expect(text).not.toMatch(/SMS alerts|priority support/i)
    // Reports are built, and the Terms describe them as they work: monthly, owners only, informational.
    for (const want of ['Monthly PDF uptime reports', 'put your own name and logo on them', 'not a guarantee, warranty, or certification', 'emailed only to the owners of your workspace']) expect(text).toContain(want)
    expect(screen.getAllByRole('link', { name: 'support@hawkeye.observer' })[0]).toHaveAttribute('href', 'mailto:support@hawkeye.observer')
  })
})
