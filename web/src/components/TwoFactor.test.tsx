import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { LoginPage } from '../pages/AuthPages'
import { RequireTwoFactorCard, TwoFactorCard, TwoFactorRequirementNotice } from './TwoFactor'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
const err = (status: number, code: string, message: string) => json(status, { error: { code, message } })

type Handler = (url: string, method: string, body: any) => Promise<Response> | undefined

function stub(handler: Handler, me: { signedIn?: boolean; enabled?: boolean; org?: Record<string, unknown> } = {}) {
  const calls: { url: string; method: string; body: any }[] = []
  let signedIn = me.signedIn ?? true
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(init.body as string) : undefined
    calls.push({ url, method, body })
    if (url.endsWith('/auth/login/two-factor') && method === 'POST') signedIn = true
    const r = handler(url, method, body)
    if (r) return r
    if (url.includes('/auth/me')) {
      if (!signedIn) return err(401, 'UNAUTHENTICATED', 'x')
      return json(200, {
        user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' },
        organizations: [{ id: 'o', name: 'Acme', role: 'owner', ...me.org }],
        two_factor: { enabled: me.enabled ?? false, enabled_at: null, recovery_codes_left: 0 },
      })
    }
    return json(404, {})
  }))
  return calls
}

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.pathname + l.hash}</output>
}

function mount(ui: React.ReactElement, path = '/') {
  render(
    <MemoryRouter initialEntries={[path]}>
      <AuthProvider>
        <Where />
        <Routes>
          <Route path="*" element={ui} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('signing in with two-factor sign-in', () => {
  it('asks for a code after the password, then signs in', async () => {
    const calls = stub((url, _method, body) => {
      if (url.endsWith('/auth/login')) return json(200, { two_factor_required: true, challenge: 'ch-1' })
      if (url.endsWith('/auth/login/two-factor')) {
        return body.code === '123456' ? json(200, { user: {}, used_recovery_code: false }) : err(401, 'INVALID_CODE', 'That code is not correct.')
      }
    }, { signedIn: false })
    mount(<LoginPage />, '/login')
    await userEvent.type(await screen.findByLabelText('Email'), 'me@example.com')
    await userEvent.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))

    const code = await screen.findByLabelText('Code')
    expect(code).toHaveAttribute('autocomplete', 'one-time-code')
    await userEvent.type(code, '000000')
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(await screen.findByText('That code is not correct.')).toBeInTheDocument()

    await userEvent.clear(code)
    await userEvent.type(code, '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByText('/dashboard', { selector: 'output' })
    expect(calls.filter((c) => c.url.endsWith('/auth/login/two-factor')).map((c) => c.body)).toEqual([
      { challenge: 'ch-1', code: '000000' }, { challenge: 'ch-1', code: '123456' },
    ])
  })

  it('accepts a recovery code and then shows the account page to set things right', async () => {
    stub((url) => {
      if (url.endsWith('/auth/login')) return json(200, { two_factor_required: true, challenge: 'ch-1' })
      if (url.endsWith('/auth/login/two-factor')) return json(200, { user: {}, used_recovery_code: true })
    }, { signedIn: false })
    mount(<LoginPage />, '/login')
    await userEvent.type(await screen.findByLabelText('Email'), 'me@example.com')
    await userEvent.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    await userEvent.click(await screen.findByRole('button', { name: /Use a recovery code/ }))
    await userEvent.type(screen.getByLabelText('Recovery code'), 'abcd-efgh-ijkl-mnop')
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    await screen.findByText('/account#two-factor', { selector: 'output' })
  })

  it('goes back to the password when the step timed out', async () => {
    stub((url) => {
      if (url.endsWith('/auth/login')) return json(200, { two_factor_required: true, challenge: 'ch-1' })
      if (url.endsWith('/auth/login/two-factor')) return err(401, 'SIGN_IN_EXPIRED', 'That sign-in timed out.')
    }, { signedIn: false })
    mount(<LoginPage />, '/login')
    await userEvent.type(await screen.findByLabelText('Email'), 'me@example.com')
    await userEvent.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await userEvent.click(screen.getByRole('button', { name: 'Log in' }))
    await userEvent.type(await screen.findByLabelText('Code'), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(await screen.findByRole('button', { name: 'Log in' })).toBeInTheDocument()
  })
})

describe('TwoFactorCard', () => {
  it('sets it up: password, QR code, code, then recovery codes shown once', async () => {
    let enabled = false
    const calls = stub((url, method) => {
      if (url.endsWith('/account/two-factor') && method === 'GET') return json(200, { two_factor: { enabled, enabled_at: enabled ? '2026-11-08T00:00:00Z' : null, recovery_codes_left: enabled ? 10 : 0 } })
      if (url.endsWith('/two-factor/setup')) return json(200, { setup: { secret: 'ABCDEFGHIJKLMNOP', otpauth_uri: 'otpauth://totp/x', qr_code: 'data:image/png;base64,AAAA' } })
      if (url.endsWith('/two-factor/enable')) {
        enabled = true
        return json(200, { recovery_codes: ['aaaa-bbbb-cccc-dddd', 'eeee-ffff-gggg-hhhh'] })
      }
    })
    mount(<TwoFactorCard />)
    await userEvent.click(await screen.findByRole('button', { name: 'Set up two-factor sign-in' }))
    await userEvent.type(screen.getByLabelText('Your password'), 'a-long-enough-password')
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(await screen.findByAltText(/QR code/)).toHaveAttribute('src', 'data:image/png;base64,AAAA')
    expect(screen.getByDisplayValue('ABCDEFGHIJKLMNOP')).toBeInTheDocument() // the key to type by hand
    await userEvent.type(screen.getByLabelText('Code'), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Turn on' }))
    expect(await screen.findByText('aaaa-bbbb-cccc-dddd')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'I’ve saved them' }))
    expect(screen.queryByText('aaaa-bbbb-cccc-dddd')).not.toBeInTheDocument()
    expect(await screen.findByText(/On since/)).toBeInTheDocument()
    expect(calls.find((c) => c.url.endsWith('/two-factor/setup'))!.body).toEqual({ password: 'a-long-enough-password' })
  })

  it('turns it off with the password and a code, and explains a refusal', async () => {
    stub((url, method) => {
      if (url.endsWith('/account/two-factor') && method === 'GET') return json(200, { two_factor: { enabled: true, enabled_at: '2026-11-01T00:00:00Z', recovery_codes_left: 2 } })
      if (url.endsWith('/two-factor/disable')) return err(409, 'TWO_FACTOR_REQUIRED_BY_ORGANIZATION', 'An organization you own requires two-factor sign-in.')
    })
    mount(<TwoFactorCard />)
    expect(await screen.findByText(/2 recovery codes left/)).toHaveTextContent('Make new ones')
    await userEvent.click(screen.getByRole('button', { name: 'Turn off' }))
    await userEvent.type(screen.getByLabelText('Your password'), 'a-long-enough-password')
    await userEvent.type(screen.getByLabelText(/Code from your authenticator app/), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Turn off two-factor sign-in' }))
    expect(await screen.findByText('An organization you own requires two-factor sign-in.')).toBeInTheDocument()
  })
})

describe('RequireTwoFactorCard', () => {
  const members = [
    { user_id: 'u', email: 'me@example.com', two_factor_enabled: true, blocked: false },
    { user_id: 'm', email: 'dev@example.com', two_factor_enabled: false, blocked: false },
  ]

  it('is off by default and needs the owner to use it first', async () => {
    stub((url) => {
      if (url.endsWith('/organizations/o/two-factor')) return json(200, { required: false, grace_days: 7, members })
    }, { enabled: false })
    mount(<RequireTwoFactorCard orgID="o" />)
    expect(await screen.findByRole('button', { name: 'Require two-factor sign-in' })).toBeDisabled()
    expect(screen.getByText(/for your own account first/)).toBeInTheDocument()
  })

  it('warns how many members are affected before requiring it', async () => {
    const calls = stub((url, method) => {
      if (url.endsWith('/organizations/o/two-factor') && method === 'GET') return json(200, { required: false, grace_days: 7, members })
      if (url.endsWith('/organizations/o/two-factor') && method === 'PUT') {
        return json(200, { required: true, grace_days: 7, members: [members[0], { ...members[1], grace_ends_at: '2026-11-15T00:00:00Z' }] })
      }
    }, { enabled: true })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    mount(<RequireTwoFactorCard orgID="o" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Require two-factor sign-in' }))
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('1 member doesn’t use two-factor sign-in yet'))
    expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ required: true })
    expect(await screen.findByText(/has until/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stop requiring it' })).toBeInTheDocument()
  })
})

describe('TwoFactorRequirementNotice', () => {
  it('warns a member during the grace period', async () => {
    stub(() => undefined, { org: { role: 'member', require_two_factor: true, two_factor_grace_ends_at: '2026-11-15T00:00:00Z', two_factor_blocked: false } })
    mount(<TwoFactorRequirementNotice />)
    expect(await screen.findByText(/requires two-factor sign-in. Set it up by/)).toBeInTheDocument()
  })

  it('says the organization is closed once the grace period is over', async () => {
    stub(() => undefined, { org: { role: 'member', require_two_factor: true, two_factor_grace_ends_at: '2026-11-01T00:00:00Z', two_factor_blocked: true } })
    mount(<TwoFactorRequirementNotice />)
    expect(await screen.findByText(/it’s closed to you until you set it up/)).toBeInTheDocument()
  })

  it('says nothing to someone who uses it', async () => {
    stub(() => undefined, { enabled: true, org: { require_two_factor: true } })
    mount(<TwoFactorRequirementNotice />)
    await screen.findByTestId('where')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
