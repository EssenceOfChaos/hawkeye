import { afterEach, describe, expect, it, vi } from 'vitest'
import { redirectTo } from './navigation'

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
