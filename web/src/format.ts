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

export interface ReportMonth {
  /** "2026-09", as the API takes it. */
  value: string
  label: string
  /** The month is not over yet, so its report covers the month so far. */
  partial: boolean
}

/**
 * The calendar months (UTC, newest first) a monthly report can be made for: the current month so far, then every earlier month that lies wholly inside the
 * history the plan keeps. A month whose results were partly deleted is left out, because its report would understate the checks.
 */
export function reportMonths(retentionDays: number, now: Date = new Date()): ReportMonth[] {
  const cutoff = now.getTime() - retentionDays * 24 * 60 * 60 * 1000
  const out: ReportMonth[] = []
  for (let i = 0; i < 14; i++) {
    const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - i, 1))
    if (i > 0 && start.getTime() < cutoff) break
    const value = `${start.getUTCFullYear()}-${String(start.getUTCMonth() + 1).padStart(2, '0')}`
    const label = start.toLocaleString('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' })
    out.push({ value, label: i === 0 ? `${label} (so far)` : label, partial: i === 0 })
  }
  return out
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
