// Uptime SLA arithmetic. A year is 365 days; a quarter is a quarter of that and a month a twelfth (so 99.9% allows 43 min 48 s a month).
// Other calculators count a 30-day month or a 365.25-day year and differ by a few percent: the page says which one this uses.

export const SECONDS_PER = {
  day: 86_400,
  week: 7 * 86_400,
  month: (365 * 86_400) / 12,
  quarter: (365 * 86_400) / 4,
  year: 365 * 86_400,
} as const

export type Period = keyof typeof SECONDS_PER

export const PERIODS: readonly { key: Period; label: string }[] = [
  { key: 'day', label: 'Per day' },
  { key: 'week', label: 'Per week' },
  { key: 'month', label: 'Per month' },
  { key: 'quarter', label: 'Per quarter' },
  { key: 'year', label: 'Per year' },
]

/** The uptime targets people ask about most. */
export const COMMON_TARGETS = [99, 99.5, 99.9, 99.95, 99.99, 99.999] as const

/** Seconds of downtime a target allows in a period. */
export function allowedDowntimeSeconds(uptimePercent: number, period: Period): number {
  return (SECONDS_PER[period] * (100 - uptimePercent)) / 100
}

/** The uptime achieved when `downtimeSeconds` of a period were down. */
export function uptimeFromDowntime(downtimeSeconds: number, period: Period): number {
  return ((SECONDS_PER[period] - downtimeSeconds) / SECONDS_PER[period]) * 100
}

/** "8 h 45 m 36 s", "43 m 48 s", "25.9 s", "864 ms". Whole seconds above a minute; the fractions only matter for the very small values. */
export function formatDuration(totalSeconds: number): string {
  if (totalSeconds <= 0) return '0 s'
  if (totalSeconds < 1) return `${Math.round(totalSeconds * 1000)} ms`
  if (totalSeconds < 60) return `${Number(totalSeconds.toFixed(totalSeconds < 10 ? 2 : 1))} s`
  let rest = Math.round(totalSeconds)
  const days = Math.floor(rest / 86_400)
  rest -= days * 86_400
  const hours = Math.floor(rest / 3600)
  rest -= hours * 3600
  const minutes = Math.floor(rest / 60)
  const seconds = rest - minutes * 60
  // Days are only worth showing when they are more than a count of hours, so the yearly figure for 99% reads "3 d 15 h 36 m".
  return [days && `${days} d`, hours && `${hours} h`, minutes && `${minutes} m`, seconds && `${seconds} s`].filter(Boolean).join(' ')
}

/** Reads a number the visitor typed. Returns null for anything that is not a finite number (empty, "abc", "1e999"). */
export function parseNumber(text: string): number | null {
  const t = text.trim().replace(/,/g, '')
  if (t === '' || !/^[+-]?(\d+\.?\d*|\.\d+)$/.test(t)) return null
  const n = Number(t)
  return Number.isFinite(n) ? n : null
}
