// Downtime cost arithmetic. Revenue is assumed to arrive evenly across the business hours given, which is the simplest honest model:
// a real outage at peak time costs more, one overnight costs less.

export interface CostInput {
  /** Average revenue per day, in dollars. */
  dailyRevenue: number
  /** Hours per day the business takes orders (1 to 24). */
  businessHours: number
  /** Length of the outage in hours (it may be longer than a day). */
  outageHours: number
  /** Share of revenue the outage stops, 0 to 100 (a checkout outage is 100; a broken blog is far less). */
  percentAffected: number
  /** Other cost for each hour of outage (staff time, support, credits). Optional. */
  otherCostPerHour: number
  /** How many outages like this a year, to project an annual exposure. */
  outagesPerYear: number
}

export interface CostResult {
  revenueLost: number
  otherCost: number
  total: number
  /** Revenue stopped for each minute of the outage, before other costs. */
  revenueLostPerMinute: number
  /** total × outages per year. */
  annualExposure: number
}

/**
 * Hours of outage are measured against business hours, so an 8-hour outage of a business open 8 hours a day loses a whole day of revenue.
 * Revenue per hour = daily revenue ÷ business hours.
 */
export function estimateDowntimeCost(i: CostInput): CostResult {
  const perHour = i.dailyRevenue / i.businessHours
  const revenueLost = perHour * i.outageHours * (i.percentAffected / 100)
  const otherCost = i.otherCostPerHour * i.outageHours
  const total = revenueLost + otherCost
  return {
    revenueLost,
    otherCost,
    total,
    revenueLostPerMinute: (perHour * (i.percentAffected / 100)) / 60,
    annualExposure: total * i.outagesPerYear,
  }
}

export interface Range {
  min: number
  max: number
}

export const LIMITS = {
  dailyRevenue: { min: 0, max: 1_000_000_000 },
  businessHours: { min: 1, max: 24 },
  outageHours: { min: 0, max: 24 * 365 },
  percentAffected: { min: 0, max: 100 },
  otherCostPerHour: { min: 0, max: 100_000_000 },
  outagesPerYear: { min: 0, max: 10_000 },
} as const satisfies Record<keyof CostInput, Range>

export function inRange(n: number, r: Range): boolean {
  return n >= r.min && n <= r.max
}
