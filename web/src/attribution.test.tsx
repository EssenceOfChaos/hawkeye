import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from './auth'
import { addressIsShareable, safeLocation, setChoice } from './analytics'
import { attributionForSignup, campaignFrom, rememberArrival } from './attribution'

const at = (address: string, referrer = '') => {
  vi.stubGlobal('location', new URL(address))
  Object.defineProperty(document, 'referrer', { value: referrer, configurable: true })
}
const remembered = () => JSON.parse(localStorage.getItem('hawkeye_first_touch') ?? 'null')

beforeEach(() => {
  localStorage.clear()
  for (const c of document.cookie.split('; ')) document.cookie = `${c.split('=')[0]}=; Max-Age=0; Path=/`
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('remembering how a visitor found HawkEye', () => {
  it('keeps a Product Hunt ?ref= as the source, with the linking site and landing page', () => {
    at('https://hawkeye.observer/?ref=producthunt', 'https://www.producthunt.com/')
    rememberArrival(true)
    expect(remembered()).toMatchObject({ source: 'producthunt', medium: 'referral', referrer_host: 'www.producthunt.com', landing_path: '/' })
  })

  it('records the kind of ad click, never the click ID', () => {
    at('https://hawkeye.observer/monitoring?gclid=Cj0KCQ-secret-click-id&utm_campaign=Uptime%20US')
    rememberArrival(true)
    expect(remembered()).toMatchObject({ source: 'google', medium: 'cpc', campaign: 'Uptime US', click_id: 'google_ads', landing_path: '/monitoring' })
    expect(localStorage.getItem('hawkeye_first_touch')).not.toContain('secret-click-id')
  })

  it('never keeps a token, an email address or anything outside the tags', () => {
    at('https://hawkeye.observer/reset-password?token=one-time-secret&utm_source=a%40b.com&utm_medium=email')
    rememberArrival(true)
    const raw = localStorage.getItem('hawkeye_first_touch')!
    expect(raw).not.toContain('one-time-secret')
    expect(raw).not.toContain('a@b.com')
    expect(remembered()).toMatchObject({ medium: 'email', landing_path: '/reset-password' })
    expect(remembered().source).toBeUndefined()
  })

  it('keeps the first campaign; a later plain visit does not replace it, but a campaign replaces a plain first visit', () => {
    at('https://hawkeye.observer/docs', 'https://news.ycombinator.com/item?id=1')
    rememberArrival(true)
    expect(remembered()).toMatchObject({ referrer_host: 'news.ycombinator.com', landing_path: '/docs' })
    at('https://hawkeye.observer/?utm_source=newsletter&utm_medium=email')
    rememberArrival(true)
    expect(remembered()).toMatchObject({ source: 'newsletter', medium: 'email' })
    at('https://hawkeye.observer/?ref=producthunt')
    rememberArrival(true)
    at('https://hawkeye.observer/pricing')
    rememberArrival(true)
    expect(remembered()).toMatchObject({ source: 'newsletter' })
  })

  it('ignores links from HawkEye itself', () => {
    at('https://hawkeye.observer/register', 'https://hawkeye.observer/')
    rememberArrival(true)
    expect(remembered().referrer_host).toBeUndefined()
  })

  it('remembers nothing while analytics is off, and turning analytics off forgets it', () => {
    at('https://hawkeye.observer/?ref=producthunt')
    rememberArrival(false)
    expect(remembered()).toBeNull()
    rememberArrival(true)
    expect(attributionForSignup(true)).toMatchObject({ source: 'producthunt' })
    expect(attributionForSignup(false)).toBeUndefined()
    setChoice('off')
    expect(remembered()).toBeNull()
  })

  it('forgets after 90 days', () => {
    vi.useFakeTimers()
    at('https://hawkeye.observer/?ref=producthunt')
    rememberArrival(true)
    vi.setSystemTime(Date.now() + 91 * 24 * 60 * 60 * 1000)
    expect(attributionForSignup(true)).toBeUndefined()
  })

  it('reads utm tags before ?ref=, and ad clicks last', () => {
    expect(campaignFrom('?utm_source=twitter&ref=producthunt')).toEqual({ source: 'twitter' })
    expect(campaignFrom('?ref=ProductHunt&utm_medium=launch')).toEqual({ source: 'producthunt', medium: 'launch' })
    expect(campaignFrom('?msclkid=x')).toEqual({ source: 'bing', medium: 'cpc', click_id: 'microsoft_ads' })
    expect(campaignFrom('')).toEqual({})
  })
})

describe('?ref= in analytics', () => {
  it('is passed to Google Analytics as utm_source with utm_medium=referral, and lets the OpenAI pixel load', () => {
    at('https://hawkeye.observer/?ref=producthunt')
    expect(Object.fromEntries(new URL(safeLocation('/')).searchParams)).toEqual({ utm_source: 'producthunt', utm_medium: 'referral' })
    expect(addressIsShareable()).toBe(true)
    at('https://hawkeye.observer/?ref=producthunt&utm_source=x')
    expect(new URL(safeLocation('/')).searchParams.get('utm_source')).toBe('x')
  })

  it('is dropped, and keeps the pixel off, when it is not a plain label', () => {
    at('https://hawkeye.observer/?ref=a%40b.com')
    expect(safeLocation('/')).toBe('https://hawkeye.observer/')
    expect(addressIsShareable()).toBe(false)
  })
})

describe('signing up', () => {
  function setup() {
    const bodies: Record<string, unknown>[] = []
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      if (url.includes('/auth/register')) {
        bodies.push(JSON.parse(String(init?.body)))
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      return Promise.resolve(new Response(JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'x' } }), { status: 401, headers: { 'Content-Type': 'application/json' } }))
    }))
    document.cookie = 'csrf_token=t; path=/'
    function Signup() {
      const { register } = useAuth()
      return <button onClick={() => void register('new@example.com', 'a-long-password')}>sign up</button>
    }
    render(<AuthProvider><Signup /></AuthProvider>)
    return bodies
  }

  it('sends what was remembered, then forgets it', async () => {
    at('https://hawkeye.observer/?ref=producthunt')
    rememberArrival(true)
    const bodies = setup()
    await userEvent.click(screen.getByRole('button', { name: 'sign up' }))
    await vi.waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0].attribution).toMatchObject({ source: 'producthunt', medium: 'referral', landing_path: '/' })
    await vi.waitFor(() => expect(remembered()).toBeNull())
  })

  it('sends nothing while analytics is off', async () => {
    at('https://hawkeye.observer/?ref=producthunt')
    rememberArrival(true)
    document.cookie = 'hawkeye_analytics=off; path=/'
    const bodies = setup()
    await userEvent.click(screen.getByRole('button', { name: 'sign up' }))
    await vi.waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0].attribution).toBeUndefined()
  })
})
