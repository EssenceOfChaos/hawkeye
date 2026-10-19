import { describe, expect, it } from 'vitest'
import { availableRanges, formatMoney, formatMs, formatUptime } from './format'

describe('format', () => {
  it('formats uptime without false precision or rounding up', () => {
    expect(formatUptime(100)).toBe('100%')
    expect(formatUptime(99.966)).toBe('99.966%')
    expect(formatUptime(99.9)).toBe('99.9%')
    expect(formatUptime(90)).toBe('90%')
  })
  it('formats response times', () => {
    expect(formatMs(null)).toBe('—')
    expect(formatMs(250)).toBe('250 ms')
    expect(formatMs(1500)).toBe('1.50 s')
  })
  it('offers only ranges within the plan history', () => {
    expect(availableRanges(7)).toEqual(['24h', '7d'])
    expect(availableRanges(90)).toEqual(['24h', '7d', '30d', '90d'])
    expect(availableRanges(365)).toHaveLength(5)
  })
  it('shows plan prices exactly, never rounded to the nearest dollar', () => {
    expect(formatMoney(0)).toBe('$0')
    expect(formatMoney(1900)).toBe('$19')
    expect(formatMoney(5900)).toBe('$59')
    expect(formatMoney(1999)).toBe('$19.99')
    expect(formatMoney(2050)).toBe('$20.50')
    expect(formatMoney(99)).toBe('$0.99')
    expect(formatMoney(100000)).toBe('$1,000')
  })
})
