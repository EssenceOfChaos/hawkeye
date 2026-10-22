import { describe, expect, it } from 'vitest'
import { statusLabel } from './httpStatus'

describe('statusLabel', () => {
  it.each([[200, '200 OK'], [404, '404 Not Found'], [503, '503 Service Unavailable'], [522, '522 Connection Timed Out'], [299, '299']])('labels %s', (code, text) => {
    expect(statusLabel(code)).toBe(text)
  })
})
