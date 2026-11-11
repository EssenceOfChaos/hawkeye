import { describe, expect, it } from 'vitest'
import { days, downSeconds, engineOK, percent, step, uptime, type State } from './logic'
import { renderPage, type Snapshot } from './page'

const fresh: State = { failures: 0, firstFailureAt: null, downSince: null }

describe('outages', () => {
  it('needs two failures in a row, and dates the outage from the first', () => {
    let s = step(fresh, false, 60)
    expect(s.opened).toBeUndefined()
    s = step(s.state, false, 120)
    expect(s.opened).toBe(60)
    s = step(s.state, false, 180)
    expect(s.opened).toBeUndefined() // still the same outage
    s = step(s.state, true, 240)
    expect(s.closed).toBe(240)
    expect(s.state).toEqual(fresh)
  })

  it('ignores a single failure', () => {
    const s = step(step(fresh, false, 60).state, true, 120)
    expect(s.opened).toBeUndefined()
    expect(s.closed).toBeUndefined()
  })
})

describe('the engine', () => {
  it('is up only with a recent heartbeat that reports checks on time', () => {
    expect(engineOK(null, 1000).ok).toBe(false)
    expect(engineOK({ at: 1000, overdue_seconds: 0 }, 1100).ok).toBe(true)
    expect(engineOK({ at: 1000, overdue_seconds: 0 }, 1000 + 181).ok).toBe(false)
    expect(engineOK({ at: 1000, overdue_seconds: 301 }, 1000).ok).toBe(false)
  })
})

describe('uptime', () => {
  const day = 86_400
  it('counts only the measured part of a period, and open outages up to now', () => {
    expect(downSeconds([{ started_at: 100, ended_at: null }], 0, 160)).toBe(60)
    expect(uptime([{ started_at: day, ended_at: day + 864 }], 0, 2 * day, day)).toBeCloseTo(0.99)
    expect(uptime([], 0, 10, 20)).toBeNull()
  })

  it('never shows 100% when there was downtime', () => {
    expect(percent(0.99999)).toBe('99.99%')
    expect(percent(1)).toBe('100.00%')
    expect(percent(null)).toBe('–')
  })

  it('makes one bar per day, gray before measuring began', () => {
    const now = 10 * day + 3600
    const bars = days([{ started_at: 9 * day + 10, ended_at: 9 * day + 70 }, { started_at: 8 * day, ended_at: 8 * day + 7200 }], 4, now, 8 * day)
    expect(bars.map((b) => b.status)).toEqual(['none', 'major', 'minor', 'up'])
  })
})

describe('the page', () => {
  it('escapes what we write in incidents', () => {
    const s: Snapshot = {
      now: 1_800_000_000, measuredSince: 1_799_000_000,
      components: [{ id: 'api', name: 'API', description: 'd', downSince: 1_799_999_000, outages: [{ started_at: 1_799_999_000, ended_at: null }] }],
      incidents: [{ id: 1, title: '<script>x</script>', created_at: 1_799_999_100, resolved_at: null, updates: [{ status: 'investigating', message: 'a & b', created_at: 1_799_999_100 }] }],
    }
    const html = renderPage(s)
    expect(html).not.toContain('<script>')
    expect(html).toContain('&lt;script&gt;')
    expect(html).toContain('a &amp; b')
    expect(html).toContain('Major outage')
  })
})
