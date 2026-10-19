/** Formats a check-based uptime percentage; the API already truncates so 100 only means no failures. */
export function formatUptime(pct: number): string {
  // Up to three decimals, trailing zeros removed (99.9, 99.966, 100).
  return `${Number(pct.toFixed(3))}%`
}

export function formatMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '—'
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${ms} ms`
}

export const RANGE_LABELS: Record<string, string> = {
  '24h': 'Last 24 hours',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  '90d': 'Last 90 days',
  '365d': 'Last 365 days',
}

const RANGE_DAYS: Record<string, number> = { '24h': 1, '7d': 7, '30d': 30, '90d': 90, '365d': 365 }

/** Presets that fit within the plan's retained history. */
export function availableRanges(retentionDays: number): string[] {
  return Object.keys(RANGE_DAYS).filter((k) => RANGE_DAYS[k] <= retentionDays)
}

export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString()
}

/**
 * A plan price in US dollars from cents, shown exactly: "$0", "$19", "$19.99". Whole-dollar prices drop the cents; anything else keeps both
 * decimals, so a $19.99 plan is never displayed as $20.
 */
export function formatMoney(cents: number): string {
  const whole = cents % 100 === 0
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: whole ? 0 : 2, maximumFractionDigits: 2 }).format(cents / 100)
}
