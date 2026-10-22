import { describe, expect, it } from 'vitest'
import { allowedDowntimeSeconds, COMMON_TARGETS, formatDuration, parseNumber, uptimeFromDowntime } from './sla'

describe('allowedDowntimeSeconds', () => {
  it('matches the well-known figures for 99.9%', () => {
    expect(allowedDowntimeSeconds(99.9, 'day')).toBeCloseTo(86.4)
    expect(allowedDowntimeSeconds(99.9, 'week')).toBeCloseTo(604.8)
    expect(allowedDowntimeSeconds(99.9, 'year')).toBeCloseTo(8.76 * 3600) // 8 h 45 m 36 s
    expect(formatDuration(allowedDowntimeSeconds(99.9, 'month'))).toBe('43 m 48 s')
  })

  it('allows nothing at 100% and everything at 0%', () => {
    expect(allowedDowntimeSeconds(100, 'year')).toBe(0)
    expect(allowedDowntimeSeconds(0, 'day')).toBe(86_400)
  })

  it('allows less downtime for every higher target', () => {
    const yearly = COMMON_TARGETS.map((t) => allowedDowntimeSeconds(t, 'year'))
    expect([...yearly].sort((a, b) => b - a)).toEqual(yearly)
  })
})

describe('uptimeFromDowntime', () => {
  it('is the inverse of allowedDowntimeSeconds', () => {
    for (const t of COMMON_TARGETS) expect(uptimeFromDowntime(allowedDowntimeSeconds(t, 'month'), 'month')).toBeCloseTo(t, 9)
  })

  it('reads 100% with no downtime and 0% when the whole period was down', () => {
    expect(uptimeFromDowntime(0, 'week')).toBe(100)
    expect(uptimeFromDowntime(7 * 86_400, 'week')).toBe(0)
  })
})

describe('formatDuration', () => {
  it.each([
    [0, '0 s'],
    [0.864, '864 ms'],
    [8.64, '8.64 s'],
    [25.92, '25.9 s'],
    [60, '1 m'],
  ])('formats %s seconds', (seconds, text) => {
    expect(formatDuration(seconds)).toBe(text)
  })

  it('formats longer spans in days, hours, minutes and seconds', () => {
    expect(formatDuration(allowedDowntimeSeconds(99.9, 'year'))).toBe('8 h 45 m 36 s')
    expect(formatDuration(allowedDowntimeSeconds(99, 'year'))).toBe('3 d 15 h 36 m')
    expect(formatDuration(3600)).toBe('1 h')
  })
})

describe('parseNumber', () => {
  it.each([
    ['99.9', 99.9],
    [' 12 ', 12],
    ['1,250.50', 1250.5],
    ['.5', 0.5],
    ['-3', -3],
  ])('reads %j', (text, n) => expect(parseNumber(text)).toBe(n))

  it.each(['', '  ', 'abc', '1e5', '12abc', '1.2.3', '--1', '$5', 'Infinity'])('rejects %j', (text) => expect(parseNumber(text)).toBeNull())
})
