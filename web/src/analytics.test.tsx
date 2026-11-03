import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from './auth'
import { addressIsShareable, analyticsEnabled, initAnalytics, readChoice, safeLocation, setChoice, trackEvent, trackPageView, trackRegistration, trackSubscription } from './analytics'
import { CookieBanner, CookieSettingsButton } from './components/CookieBanner'

type G = { gtag?: unknown; dataLayer?: unknown[]; oaiq?: { q?: unknown[] } }
const win = window as unknown as G
const googleScripts = () => [...document.querySelectorAll('script')].filter((s) => s.src.includes('googletagmanager'))
const openAIScripts = () => [...document.querySelectorAll('script')].filter((s) => s.src.includes('bzrcdn.openai.com'))
/** The calls queued for the OpenAI SDK, as arrays. */
const openAICalls = () => (win.oaiq?.q ?? []).map((a) => Array.from(a as ArrayLike<unknown>))
const openAIEvents = () => openAICalls().filter((c) => c[0] === 'measure').map((c) => c[1])
const at = (address: string) => vi.stubGlobal('location', new URL(address))
const sentEvents = () => JSON.stringify((win.dataLayer ?? []).map((a) => Array.from(a as ArrayLike<unknown>)))

function clearCookies() {
  for (const c of document.cookie.split('; ')) document.cookie = `${c.split('=')[0]}=; Max-Age=0; Path=/`
}

// jsdom's hostname is "localhost", which is deliberately excluded from analytics, so these tests pretend to be the real site.
function asProductionHost() {
  vi.stubGlobal('location', new URL('https://hawkeye.observer/reset-password?token=secret#frag'))
}

beforeEach(() => {
  clearCookies()
  document.head.querySelectorAll('script').forEach((s) => s.remove())
  delete win.gtag
  delete win.dataLayer
  delete win.oaiq
  localStorage.clear()
  asProductionHost()
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const renderPanel = () => render(<MemoryRouter><CookieBanner /><CookieSettingsButton /></MemoryRouter>)

describe('analytics (on by default, opt-out)', () => {
  it('loads the Google tag by default, with no banner in the way', () => {
    renderPanel()
    initAnalytics()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(googleScripts()).toHaveLength(1)
    expect(googleScripts()[0].src).toContain('id=G-ZNT8EMTFGY')
  })

  it('sends page views with only the origin and path, never the token or fragment', () => {
    initAnalytics()
    trackPageView('/reset-password')
    const sent = sentEvents()
    expect(sent).toContain('page_view')
    expect(sent).toContain('https://hawkeye.observer/reset-password')
    expect(sent).not.toContain('secret')
    expect(sent).not.toContain('frag')
  })

  it('treats a browser sending Global Privacy Control as opted out, unless the visitor chooses otherwise', () => {
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { globalPrivacyControl: true }))
    initAnalytics()
    expect(analyticsEnabled()).toBe(false)
    expect(googleScripts()).toHaveLength(0)
    setChoice('on')
    expect(analyticsEnabled()).toBe(true)
    expect(googleScripts()).toHaveLength(1)
  })

  it('Cookie settings shows the state, and "Turn off analytics" stops tracking and deletes the cookies', async () => {
    initAnalytics()
    document.cookie = '_ga=GA1.1.1; Path=/'
    document.cookie = '_ga_ZNT8EMTFGY=GS1; Path=/'
    renderPanel()
    await userEvent.click(screen.getByRole('button', { name: 'Cookie settings' }))
    expect(screen.getByRole('dialog', { name: 'Cookie settings' })).toHaveTextContent('currently on')
    await userEvent.click(screen.getByRole('button', { name: 'Turn off analytics' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(readChoice()).toBe('off')
    expect(document.cookie).not.toContain('_ga')
    const before = sentEvents()
    trackPageView('/dashboard')
    expect(sentEvents()).toBe(before)
  })

  it('an opted-out visitor is not tracked on the next visit, and can turn analytics back on', async () => {
    setChoice('off')
    document.head.querySelectorAll('script').forEach((s) => s.remove())
    delete win.gtag
    initAnalytics()
    expect(googleScripts()).toHaveLength(0)
    renderPanel()
    await userEvent.click(screen.getByRole('button', { name: 'Cookie settings' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('currently off')
    await userEvent.click(screen.getByRole('button', { name: 'Keep analytics on' }))
    expect(readChoice()).toBe('on')
    expect(googleScripts()).toHaveLength(1)
  })

  it('forwards campaign parameters and ad click IDs but never a token or anything else', () => {
    vi.stubGlobal('location', new URL('https://hawkeye.observer/monitoring?utm_source=google&utm_medium=cpc&utm_campaign=launch&gclid=abc123&token=secret&email=a%40b.com&next=%2Fx#frag'))
    const url = new URL(safeLocation('/monitoring'))
    expect(url.origin + url.pathname).toBe('https://hawkeye.observer/monitoring')
    expect(Object.fromEntries(url.searchParams)).toEqual({ utm_source: 'google', utm_medium: 'cpc', utm_campaign: 'launch', gclid: 'abc123' })
    expect(url.hash).toBe('')
    initAnalytics()
    trackPageView('/monitoring')
    const sent = sentEvents()
    expect(sent).toContain('utm_campaign=launch')
    expect(sent).not.toContain('secret')
    expect(sent).not.toContain('a%40b.com')
  })

  it('a link with only a token sends a clean address', () => {
    expect(safeLocation('/reset-password')).toBe('https://hawkeye.observer/reset-password')
  })

  it('sends named events only while analytics is on', () => {
    initAnalytics()
    trackEvent('sign_up', { method: 'email' })
    expect(sentEvents()).toContain('sign_up')
    setChoice('off')
    const before = sentEvents()
    trackEvent('sign_up', { method: 'email' })
    expect(sentEvents()).toBe(before)
  })

  it('records a sign_up event when registration succeeds, and none when it fails', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
      if (url.includes('/auth/me')) return json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })
      if (url.includes('/auth/register')) return init?.body && String(init.body).includes('taken') ? json(409, { error: { code: 'CONFLICT', message: 'x' } }) : Promise.resolve(new Response(null, { status: 204 }))
      return json(404, {})
    }))
    document.cookie = 'csrf_token=t; path=/'
    initAnalytics()
    function Signup() {
      const { register } = useAuth()
      return (
        <>
          <button onClick={() => void register('new@example.com', 'a-long-password')}>ok</button>
          <button onClick={() => void register('taken', 'a-long-password').catch(() => undefined)}>fail</button>
        </>
      )
    }
    render(<AuthProvider><Signup /></AuthProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'fail' }))
    await new Promise((r) => setTimeout(r, 20))
    expect(sentEvents()).not.toContain('sign_up')
    await userEvent.click(screen.getByRole('button', { name: 'ok' }))
    await vi.waitFor(() => expect(sentEvents()).toContain('sign_up'))
    expect(sentEvents()).not.toContain('new@example.com')
  })
})

describe('OpenAI Ads pixel', () => {
  it('loads with the pixel id on a safe address, with debug logging off', () => {
    at('https://hawkeye.observer/?utm_source=chatgpt&oppref=abc123')
    initAnalytics()
    expect(openAIScripts()).toHaveLength(1)
    expect(openAIScripts()[0].src).toBe('https://bzrcdn.openai.com/sdk/oaiq.min.js')
    expect(openAICalls()).toContainEqual(['init', { pixelId: 'BQzFQqkTH5HXxdU4vT171u', debug: false }])
  })

  it('turns on debug logging only in a browser that asked for it', () => {
    localStorage.setItem('hawkeye_oaiq_debug', '1')
    at('https://hawkeye.observer/')
    initAnalytics()
    expect(openAICalls()).toContainEqual(['init', { pixelId: 'BQzFQqkTH5HXxdU4vT171u', debug: true }])
  })

  it('never loads or sends anything while the address carries a token, and starts on the next safe page', () => {
    initAnalytics() // on /reset-password?token=secret#frag
    trackPageView('/reset-password')
    expect(openAIScripts()).toHaveLength(0)
    expect(win.oaiq).toBeUndefined()
    expect(googleScripts()).toHaveLength(1) // Google is unaffected: it is only ever sent the cleaned address

    at('https://hawkeye.observer/dashboard')
    trackPageView('/dashboard')
    expect(openAIScripts()).toHaveLength(1)
    expect(openAIEvents()).toEqual(['page_viewed'])

    at('https://hawkeye.observer/accept-invite?token=abc')
    trackPageView('/accept-invite')
    expect(openAIEvents()).toEqual(['page_viewed'])
  })

  it('allows only campaign tags, its click id and harmless app parameters in the address', () => {
    for (const ok of ['https://hawkeye.observer/', 'https://hawkeye.observer/?oppref=x&utm_campaign=y', 'https://hawkeye.observer/billing?checkout=success', 'https://hawkeye.observer/billing?interval=year']) {
      at(ok)
      expect(addressIsShareable()).toBe(true)
    }
    for (const bad of ['https://hawkeye.observer/verify-email?token=t', 'https://hawkeye.observer/x?email=a@b.co', 'https://hawkeye.observer/#access_token=t', 'https://hawkeye.observer/lifecycle/unsubscribe?token=t&list=onboarding']) {
      at(bad)
      expect(addressIsShareable()).toBe(false)
    }
  })

  it('reports a registration and a paid subscription with the amount in cents and a dedup id', () => {
    at('https://hawkeye.observer/billing?checkout=success')
    initAnalytics()
    trackRegistration()
    trackSubscription(59000, 'agency', 'year', '2027-11-02T00:00:00Z')
    const measured = openAICalls().filter((c) => c[0] === 'measure')
    expect(measured).toEqual([
      ['measure', 'registration_completed', { type: 'customer_action' }],
      ['measure', 'subscription_created', { type: 'plan_enrollment', plan_id: 'agency_year', amount: 59000, currency: 'USD' }, { event_id: '2027-11-02T00:00:00Z' }],
    ])
  })

  it('stays off for an opted-out visitor, and turning analytics off withdraws consent and deletes its cookies', () => {
    at('https://hawkeye.observer/')
    vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { globalPrivacyControl: true }))
    initAnalytics()
    trackPageView('/')
    expect(openAIScripts()).toHaveLength(0)
    vi.unstubAllGlobals()

    at('https://hawkeye.observer/')
    setChoice('on')
    expect(openAIScripts()).toHaveLength(1)
    document.cookie = '__oppref=abc; Path=/'
    document.cookie = '__obref=def; Path=/'
    setChoice('off')
    expect(openAICalls()).toContainEqual(['consent', false])
    expect(document.cookie).not.toMatch(/__oppref|__obref/)
    const before = openAIEvents().length
    trackPageView('/')
    trackSubscription(1900, 'pro', 'month', 'x')
    expect(openAIEvents()).toHaveLength(before)

    setChoice('on')
    expect(openAICalls().at(-1)).toEqual(['consent', true])
  })
})
