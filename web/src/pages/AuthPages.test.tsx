import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { ConfirmAlertAddressPage, ResetPasswordPage, VerifyEmailPage } from './AuthPages'

afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.pathname + l.search}</output>
}

function mount(path: string) {
  const posts: { url: string; body: any }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) return json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
    if (init?.method === 'POST') {
      posts.push({ url, body: init.body ? JSON.parse(init.body as string) : undefined })
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    return json(404, {})
  }))
  render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <Where />
        <Routes>
          <Route path="/reset-password" element={<ResetPasswordPage />} />
          <Route path="/verify-email" element={<VerifyEmailPage />} />
          <Route path="/confirm-alert-address" element={<ConfirmAlertAddressPage />} />
          <Route path="*" element={<p>elsewhere</p>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
  return posts
}

describe('one-time tokens in links', () => {
  it('removes the token from the address bar but still uses it (password reset)', async () => {
    const posts = mount('/reset-password?token=SECRET123')
    await userEvent.type(await screen.findByLabelText('New password'), 'a-brand-new-password')
    expect(screen.getByTestId('where')).toHaveTextContent('/reset-password')
    expect(screen.getByTestId('where')).not.toHaveTextContent('SECRET123')
    await userEvent.click(screen.getByRole('button', { name: 'Update password' }))
    expect(posts[0]).toEqual({ url: '/api/v1/auth/reset-password', body: { token: 'SECRET123', password: 'a-brand-new-password' } })
  })

  it('removes the token and verifies the email (verification link)', async () => {
    const posts = mount('/verify-email?token=VERIFY456')
    expect(await screen.findByText(/Your email address is verified/)).toBeInTheDocument()
    expect(screen.getByTestId('where')).not.toHaveTextContent('VERIFY456')
    expect(posts.some((p) => p.url.endsWith('/auth/verify-email') && p.body.token === 'VERIFY456')).toBe(true)
  })

  it('removes the token and confirms the alert address', async () => {
    const posts = mount('/confirm-alert-address?token=ALERT789')
    expect(await screen.findByText(/will now receive HawkEye alerts/)).toBeInTheDocument()
    expect(screen.getByTestId('where')).not.toHaveTextContent('ALERT789')
    expect(posts[0].body).toEqual({ token: 'ALERT789' })
  })
})
