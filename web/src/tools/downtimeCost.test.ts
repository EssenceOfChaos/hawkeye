import { describe, expect, it } from 'vitest'
import { estimateDowntimeCost, inRange, LIMITS, type CostInput } from './downtimeCost'

const base: CostInput = { dailyRevenue: 2400, businessHours: 24, outageHours: 2, percentAffected: 100, otherCostPerHour: 0, outagesPerYear: 1 }

describe('estimateDowntimeCost', () => {
  it('spreads daily revenue over the business hours', () => {
    const r = estimateDowntimeCost(base) // $100 an hour
    expect(r.revenueLost).toBe(200)
    expect(r.total).toBe(200)
    expect(r.revenueLostPerMinute).toBeCloseTo(100 / 60)
  })

  it('loses a whole day of revenue when the outage lasts the whole business day', () => {
    expect(estimateDowntimeCost({ ...base, businessHours: 8, outageHours: 8 }).revenueLost).toBe(2400)
  })

  it('counts only the share of revenue the outage stops', () => {
    expect(estimateDowntimeCost({ ...base, percentAffected: 25 }).revenueLost).toBe(50)
  })

  it('adds the other hourly cost and projects a year of outages', () => {
    const r = estimateDowntimeCost({ ...base, otherCostPerHour: 50, outagesPerYear: 4 })
    expect(r.otherCost).toBe(100)
    expect(r.total).toBe(300)
    expect(r.annualExposure).toBe(1200)
  })

  it('handles an outage longer than a day', () => {
    expect(estimateDowntimeCost({ ...base, outageHours: 48 }).revenueLost).toBe(4800)
  })

  it('costs nothing for no outage, no revenue or no affected share', () => {
    expect(estimateDowntimeCost({ ...base, outageHours: 0 }).total).toBe(0)
    expect(estimateDowntimeCost({ ...base, dailyRevenue: 0 }).total).toBe(0)
    expect(estimateDowntimeCost({ ...base, percentAffected: 0 }).total).toBe(0)
  })
})

describe('inRange', () => {
  it('accepts the limits themselves and rejects values outside them', () => {
    expect(inRange(1, LIMITS.businessHours)).toBe(true)
    expect(inRange(24, LIMITS.businessHours)).toBe(true)
    expect(inRange(0, LIMITS.businessHours)).toBe(false) // would divide by zero
    expect(inRange(25, LIMITS.businessHours)).toBe(false)
    expect(inRange(-1, LIMITS.dailyRevenue)).toBe(false)
  })
})
