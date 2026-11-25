import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from './AuthProvider'
import { AccountPage } from './pages/AccountPage'
import { LoginPage, RegisterPage } from './pages/AuthPages'
import { lastUsed, readSocialReturn, rememberSignIn } from './socialSignIn'
import { answerConfirm } from './testConfirm'

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

let assign: ReturnType<typeof vi.fn>
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
  localStorage.clear()
  assign = vi.fn()
  vi.spyOn(window, 'location', 'get').mockReturnValue({ ...window.location, assign } as Location)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.pathname + l.search + l.hash}</output>
}

interface Opts {
  providers?: string[]
  signedIn?: boolean
  methods?: unknown
  post?: (url: string, body: any) => Promise<Response> | undefined
}

function mount(path: string, o: Opts = {}) {
  const posts: { url: string; body: any }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.includes('/auth/me')) {
      return o.signedIn
        ? json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: 'owner' }] })
        : json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
    }
    if (url.includes('/auth/social/providers')) return json(200, { providers: o.providers ?? ['github', 'google', 'x'] })
    if (url.includes('/account/sign-in-methods') && init?.method !== 'DELETE') return json(200, o.methods)
    if (url.includes('/account/two-factor')) return json(200, { two_factor: { enabled: false, recovery_codes_left: 0 } })
    if (url.includes('/account/email-preferences')) return json(200, { email_preferences: { weekly_summary: true, onboarding: true } })
    if (url.includes('/account/security-log')) return json(200, { events: [], next_before: null, categories: ['account'], retention_days: 365 })
    if (init?.method === 'POST' || init?.method === 'DELETE') {
      const body = init.body ? JSON.parse(init.body as string) : undefined
      posts.push({ url: `${init.method} ${url}`, body })
      const r = o.post?.(url, body)
      if (r) return r
      if (url.endsWith('/start')) return json(200, { url: 'https://accounts.example/auth?state=x' })
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    return json(404, {})
  }))
  render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <Where />
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
          <Route path="/account" element={<AccountPage />} />
          <Route path="*" element={<p>elsewhere</p>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
  return posts
}

describe('sign in with Google, GitHub and X', () => {
  it('offers every provider next to the email form, in order, and starts at the provider', async () => {
    const posts = mount('/login')
    const google = await screen.findByRole('button', { name: 'Continue with Google' })
    expect(screen.getAllByRole('button', { name: /^Continue with/ }).map((b) => b.textContent)).toEqual(['Continue with Google', 'Continue with GitHub', 'Continue with X'])
    expect(screen.getByText('or')).toBeInTheDocument()
    // The email form is still there and works as before.
    expect(screen.getByLabelText('Email')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()

    await userEvent.click(google)
    expect(posts[0].url).toBe('POST /api/v1/auth/social/google/start')
    expect(posts[0].body).toMatchObject({ intent: 'signin', next: '/dashboard' })
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.example/auth?state=x'))
  })

  it('shows no buttons and no "or" when the server offers no provider', async () => {
    mount('/register', { providers: [] })
    expect(await screen.findByRole('button', { name: 'Create free account' })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('button', { name: /Continue with/ })).not.toBeInTheDocument())
    expect(screen.queryByText('or')).not.toBeInTheDocument()
  })

  it('marks the way this browser signed in last', async () => {
    rememberSignIn('x')
    mount('/login')
    expect(await screen.findByRole('button', { name: 'Continue with X Last used' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Continue with GitHub' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Continue with Google' })).toBeInTheDocument()
  })

  it('marks the email form when the password was last used, and remembers a password sign-in', async () => {
    rememberSignIn('password')
    mount('/login', { post: (url) => (url.endsWith('/auth/login') ? json(200, { user: {} }) : undefined) })
    await screen.findByRole('button', { name: 'Continue with Google' })
    expect(screen.getByText('Last used')).toBeInTheDocument()
    localStorage.clear()
    await userEvent.type(screen.getByLabelText('Email'), 'me@example.com')
    await userEvent.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(lastUsed()).toBe('password'))
  })

  it('explains why a provider sign-in failed', async () => {
    mount('/login?error=email_not_verified')
    expect(await screen.findByRole('alert')).toHaveTextContent('doesn’t have a verified email address')
  })

  it('continues to the code step after a provider, and remembers the provider', async () => {
    const posts = mount('/login#challenge=CH1&via=google&next=%2Fmonitors', {
      post: (url) => (url.endsWith('/login/two-factor') ? json(200, { user: {} }) : undefined),
    })
    expect(await screen.findByRole('heading', { name: 'Enter your code' })).toBeInTheDocument()
    // The challenge is taken out of the address bar.
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/login$/))
    await userEvent.type(screen.getByLabelText('Code'), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/monitors'))
    expect(posts.find((p) => p.url.endsWith('/login/two-factor'))?.body).toEqual({ challenge: 'CH1', code: '123456' })
    expect(lastUsed()).toBe('google')
  })

  it('reads what the redirect back says', () => {
    expect(readSocialReturn('#signed-in=github&signed-up=ev1')).toMatchObject({ signedIn: 'github', signedUpEventId: 'ev1' })
    expect(readSocialReturn('#confirmed=google').confirmed).toBe('google')
    expect(readSocialReturn('#signed-in=x&via=x').via).toBe('x')
    expect(readSocialReturn('#signed-in=twitter').signedIn).toBeUndefined()
  })
})

describe('sign-in methods in account settings', () => {
  const both = {
    has_password: true, providers: ['github', 'google'],
    identities: [{ provider: 'github', email: 'me@users.example', connected_at: '2026-11-20T00:00:00Z', last_used_at: '2026-11-21T00:00:00Z' }],
  }

  it('lists the password and providers, connects one and disconnects another', async () => {
    const posts = mount('/account', { signedIn: true, methods: both })
    const card = within(await screen.findByRole('region', { name: 'Sign-in methods' }))
    expect(card.getByText('me@users.example', { exact: false })).toBeInTheDocument()
    await userEvent.click(card.getByRole('button', { name: 'Connect Google' }))
    expect(posts.at(-1)).toMatchObject({ url: 'POST /api/v1/auth/social/google/start', body: { intent: 'connect' } })
    await userEvent.click(card.getByRole('button', { name: 'Disconnect' }))
    expect(await answerConfirm('Disconnect')).toMatch(/Stop signing in with GitHub/)
    expect(posts.at(-1)?.url).toBe('DELETE /api/v1/account/sign-in-methods/github')
    // With a password, the password is still asked for where it was.
    expect(screen.getByRole('heading', { name: 'Change password' })).toBeInTheDocument()
  })

  it('says a provider was connected, after coming back from it', async () => {
    mount('/account?connected=google', { signedIn: true, methods: both })
    expect(await screen.findByText('Google is connected. You can use it to sign in.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/account$/))
  })

  it('without a password: the only way in can’t be removed, and sensitive actions confirm with the provider', async () => {
    const methods = { has_password: false, providers: ['github', 'google'], identities: [both.identities[0]] }
    const posts = mount('/account', { signedIn: true, methods })
    const card = within(await screen.findByRole('region', { name: 'Sign-in methods' }))
    expect(card.getByRole('button', { name: 'Disconnect' })).toBeDisabled()
    expect(screen.queryByRole('heading', { name: 'Change password' })).not.toBeInTheDocument()
    // Deleting the account asks to confirm with GitHub instead of a password.
    const del = within(screen.getByRole('region', { name: 'Delete account' }))
    expect(del.queryByLabelText('Your password')).not.toBeInTheDocument()
    await userEvent.click(del.getByRole('button', { name: 'Confirm with GitHub' }))
    expect(posts.at(-1)).toMatchObject({ url: 'POST /api/v1/auth/social/github/start', body: { intent: 'confirm', next: '/account?for=delete' } })
  })

  it('after confirming, lets an account without a password set one', async () => {
    const methods = { has_password: false, providers: ['github'], identities: [both.identities[0]] }
    const posts = mount('/account?for=password#confirmed=github', { signedIn: true, methods })
    expect(await screen.findAllByText('Confirmed with GitHub. You have 10 minutes to finish.')).not.toHaveLength(0)
    await userEvent.type(screen.getByLabelText('New password'), 'a-brand-new-password')
    await userEvent.type(screen.getByLabelText('Repeat new password'), 'a-brand-new-password')
    await userEvent.click(screen.getByRole('button', { name: 'Set password' }))
    expect(posts.find((p) => p.url.endsWith('/account/password'))?.body).toEqual({ password: 'a-brand-new-password' })
  })
})
