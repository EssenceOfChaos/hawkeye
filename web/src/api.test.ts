import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from './api'

function res(status: number, body?: unknown) {
  return Promise.resolve(new Response(body === undefined ? null : JSON.stringify(body), { status }))
}

beforeEach(() => {
  document.cookie = 'csrf_token=abc123; path=/'
})
afterEach(() => {
  document.cookie = 'csrf_token=; path=/; max-age=0'
  vi.unstubAllGlobals()
})

describe('api', () => {
  it('sends the CSRF header on unsafe methods only', async () => {
    const fetchMock = vi.fn(() => res(200, {}))
    vi.stubGlobal('fetch', fetchMock)
    await api('POST', '/monitors', { a: 1 })
    await api('GET', '/monitors')
    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][]
    expect((calls[0][1].headers as Record<string, string>)['X-CSRF-Token']).toBe('abc123')
    expect((calls[1][1].headers as Record<string, string>)['X-CSRF-Token']).toBeUndefined()
  })

  it('turns error envelopes into ApiError with field details', async () => {
    vi.stubGlobal('fetch', vi.fn(() => res(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { url: 'nope' } } } })))
    const err = (await api('POST', '/monitors', {}).catch((e: unknown) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('VALIDATION_ERROR')
    expect(err.fields).toEqual({ url: 'nope' })
  })

  it('handles 204 and non-JSON error bodies', async () => {
    vi.stubGlobal('fetch', vi.fn(() => res(204)))
    expect(await api('DELETE', '/monitors/1')).toBeUndefined()
    vi.stubGlobal('fetch', vi.fn(() => res(502)))
    await expect(api('GET', '/monitors')).rejects.toMatchObject({ status: 502, code: 'UNKNOWN' })
  })
})
