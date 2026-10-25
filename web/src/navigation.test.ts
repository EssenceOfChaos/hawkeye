import { afterEach, describe, expect, it, vi } from 'vitest'
import { afterLoginPath, redirectTo } from './navigation'

afterEach(() => vi.unstubAllGlobals())

describe('redirectTo', () => {
  it('follows https URLs only', () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { assign })
    redirectTo('https://checkout.stripe.com/c/pay/cs_test')
    expect(assign).toHaveBeenCalledOnce()
    for (const bad of ['javascript:alert(1)', 'data:text/html,hi', 'http://insecure.example.com', 'not a url', '//evil.example.com']) {
      expect(() => redirectTo(bad)).toThrow()
    }
    expect(assign).toHaveBeenCalledOnce()
  })
})

describe('afterLoginPath', () => {
  it('returns to the page the visitor was sent away from', () => {
    expect(afterLoginPath({ from: '/monitors/new' })).toBe('/monitors/new')
  })

  it.each([undefined, null, {}, { from: 42 }, { from: '' }, { from: 'https://evil.example' }, { from: '//evil.example' }, { from: '/\\evil.example' }, { from: '/login' }])(
    'falls back to the dashboard for %j',
    (state) => {
      expect(afterLoginPath(state)).toBe('/dashboard')
    },
  )
})
