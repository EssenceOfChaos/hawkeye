import { describe, expect, it } from 'vitest'
import { days, downSeconds, engineOK, outageSeconds, parseWindow, percent, step, uptime, type State } from './logic'
import { FAVICON_DATA_URL, faviconBytes } from './favicon'
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
      maintenance: [],
    }
    const html = renderPage(s)
    expect(html).not.toContain('<script>')
    expect(html).toContain('&lt;script&gt;')
    expect(html).toContain('a &amp; b')
    expect(html).toContain('Major outage')
  })

  it("labels each day's bar with a tooltip of its own, not the browser's slow one", () => {
    const s: Snapshot = {
      now: 1_800_000_000, measuredSince: 1_799_000_000,
      components: [{ id: 'api', name: 'API', description: 'd', downSince: null, outages: [{ started_at: 1_799_990_000, ended_at: 1_799_990_600 }] }],
      incidents: [],
      maintenance: [],
    }
    const html = renderPage(s)
    expect(html).not.toMatch(/<li[^>]*title=/)
    expect(html.match(/<li class="[a-z]+"><span class="tip">[^<]+<\/span><\/li>/g)).toHaveLength(90)
    expect(html).toMatch(/<span class="tip">[A-Z][a-z]{2} \d+, \d{4}: down 10 min<\/span>/)
  })
})

describe('planned maintenance', () => {
  const day = 86_400
  const w = { starts_at: 1000, ends_at: 1600 } // a 10-minute window

  it('leaves downtime inside a window out, and counts what runs past its end', () => {
    expect(downSeconds([{ started_at: 1100, ended_at: 1300 }], 0, 5000, [w])).toBe(0)
    expect(downSeconds([{ started_at: 1500, ended_at: 1900 }], 0, 5000, [w])).toBe(300) // 1600 to 1900
    expect(downSeconds([{ started_at: 700, ended_at: 1100 }], 0, 5000, [w])).toBe(300) // 700 to 1000: it began before the window
    expect(outageSeconds([{ started_at: 1500, ended_at: 1900 }], 0, 5000, [w])).toEqual({ counted: 300, maintenance: 100 })
    expect(downSeconds([{ started_at: 1100, ended_at: null }], 0, 2000, [w])).toBe(400) // still down after the window: it counts
  })

  it('merges overlapping windows rather than subtracting twice', () => {
    expect(downSeconds([{ started_at: 1000, ended_at: 2000 }], 0, 5000, [w, { starts_at: 1200, ends_at: 1800 }])).toBe(200)
  })

  it('leaves windows out of the time measured, so maintenance never lowers or raises the uptime', () => {
    const period = 10 * day
    expect(uptime([{ started_at: 1100, ended_at: 1500 }], 0, period, 0, [w])).toBe(1)
    expect(uptime([{ started_at: 2000, ended_at: 2000 + 864 }], 0, period, 0, [w])).toBeCloseTo(1 - 864 / (period - 600), 10)
    expect(uptime([], 0, 600, 0, [{ starts_at: 0, ends_at: 600 }])).toBeNull()
  })

  it('shows a day whose only downtime was planned as maintenance, not as an outage', () => {
    const now = 3 * day
    const bars = days([{ started_at: day + 100, ended_at: day + 200 }, { started_at: 2 * day + 100, ended_at: 2 * day + 4000 }], 3, now, 0,
      [{ starts_at: day, ends_at: day + 600 }, { starts_at: 2 * day, ends_at: 2 * day + 600 }])
    expect(bars.map((b) => b.status)).toEqual(['maintenance', 'minor', 'up']) // the last bar is today, still empty
    expect(bars[1]).toMatchObject({ downSeconds: 3400, maintenanceSeconds: 500 }) // 100 to 600 inside the window, 600 to 4000 after it
  })

  it('accepts a window only before it starts, and only a short one', () => {
    const now = 1_800_000_000
    expect(parseWindow({ title: 'Server reboot', starts_at: now + 3600, duration_minutes: 10 }, now)).toEqual({ title: 'Server reboot', message: '', starts_at: now + 3600, ends_at: now + 4200 })
    expect(parseWindow({ title: 'Reboot', starts_at: new Date((now + 60) * 1000).toISOString(), ends_at: new Date((now + 660) * 1000).toISOString() }, now))
      .toMatchObject({ starts_at: now + 60, ends_at: now + 660 })
    expect(parseWindow({ title: 'Now', starts_at: now - 30, duration_minutes: 5 }, now)).toMatchObject({ starts_at: now }) // a little grace, never backdated
    expect(parseWindow({ title: 'Backdated', starts_at: now - 600, duration_minutes: 30 }, now)).toMatch(/in the past/)
    expect(parseWindow({ title: 'Long', starts_at: now + 60, duration_minutes: 241 }, now)).toMatch(/at most 4 hours/)
    expect(parseWindow({ title: 'Far', starts_at: now + 91 * day, duration_minutes: 10 }, now)).toMatch(/90 days/)
    expect(parseWindow({ title: 'Backwards', starts_at: now + 600, ends_at: now + 60 }, now)).toMatch(/after starts_at/)
    expect(parseWindow({ title: 'No zone', starts_at: '2026-11-16T03:00:00', duration_minutes: 10 }, now)).toMatch(/time zone/)
    expect(parseWindow({ starts_at: now + 60, duration_minutes: 10 }, now)).toMatch(/title/)
  })

  const base = (o: Partial<Snapshot>): Snapshot => ({
    now: 1_800_000_000, measuredSince: 1_799_000_000, incidents: [], maintenance: [],
    components: [{ id: 'website', name: 'Website', description: 'd', downSince: null, outages: [] }], ...o,
  })

  it('announces a window before it starts, with when it was announced', () => {
    const html = renderPage(base({ maintenance: [{ id: 1, title: 'Kernel <update>', message: 'About 2 minutes.', starts_at: 1_800_003_600, ends_at: 1_800_004_200, created_at: 1_799_999_000 }] }))
    expect(html).toContain('Planned maintenance: Kernel &lt;update&gt;')
    expect(html).toContain('<strong>Scheduled</strong>')
    expect(html).toContain('Announced ')
    expect(html).toContain('All systems operational')
  })

  it('shows downtime inside a window as maintenance, and downtime outside one as an outage', () => {
    const during = base({
      maintenance: [{ id: 1, title: 'Reboot', message: '', starts_at: 1_799_999_800, ends_at: 1_800_000_400, created_at: 1_799_990_000 }],
      components: [{ id: 'website', name: 'Website', description: 'd', downSince: 1_799_999_880, outages: [{ started_at: 1_799_999_880, ended_at: null }] }],
    })
    let html = renderPage(during)
    expect(html).toContain('Scheduled maintenance in progress')
    expect(html).toContain('Under maintenance')
    expect(html).not.toContain('Down since')
    expect(html).toContain('100.00% uptime')

    html = renderPage({ ...during, maintenance: [] })
    expect(html).toContain('Down since')
    expect(html).not.toContain('Under maintenance')
  })
})

describe('the favicon', () => {
  it('is embedded in the page and served at /favicon.ico as a real PNG', () => {
    const html = renderPage({ now: 1_800_000_000, measuredSince: null, components: [], incidents: [], maintenance: [] })
    expect(html).toContain(`<link rel="icon" type="image/png" sizes="32x32" href="${FAVICON_DATA_URL}">`)
    expect([...faviconBytes().slice(0, 8)]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]) // the PNG signature
  })
})
