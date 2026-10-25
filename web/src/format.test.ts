import { describe, expect, it } from 'vitest'
import { availableRanges, formatMoney, formatMs, formatUptime, reportMonths } from './format'

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

describe('reportMonths', () => {
  const now = new Date('2026-10-25T12:00:00Z')

  it('lists the current month so far, then whole months inside the history the plan keeps', () => {
    const months = reportMonths(90, now)
    expect(months.map((m) => m.value)).toEqual(['2026-10', '2026-09', '2026-08'])
    expect(months[0]).toMatchObject({ label: 'October 2026 (so far)', partial: true })
    expect(months[1]).toMatchObject({ label: 'September 2026', partial: false })
  })

  it('goes back about a year on a plan that keeps a year', () => {
    const months = reportMonths(365, now)
    expect(months[0].value).toBe('2026-10')
    expect(months.at(-1)?.value).toBe('2025-11')
  })

  it('always offers the current month and handles the turn of the year', () => {
    expect(reportMonths(7, now).map((m) => m.value)).toEqual(['2026-10'])
    // 60 days back from 5 Jan is 6 Nov: November is partly outside the kept history, so it is left out rather than reported short.
    expect(reportMonths(60, new Date('2027-01-05T00:00:00Z')).map((m) => m.value)).toEqual(['2027-01', '2026-12'])
  })
})
