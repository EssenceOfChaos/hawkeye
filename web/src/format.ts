import type { Timings } from './api'

/**
 * Uptime on a status page: always three decimals, truncated (never rounded up), so 99.9996% reads 99.999% and only a clean record reads 100.000%.
 * Three decimals tell 99.9% (three nines) from 99.99% (four nines) apart at a glance.
 */
export function statusUptime(pct: number): string {
  // The epsilon undoes floating-point error in the multiplication, so a value the API already truncated is never taken one step lower.
  return `${(Math.floor(pct * 1000 + 1e-6) / 1000).toFixed(3)}%`
}

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

/**
 * The address that downloads one website's monthly report. Both parts are encoded, so neither the monitor id (it comes from the page's address) nor the
 * month (it comes from a form control) can change the path or add to the query.
 */
export function reportPdfPath(monitorID: string, month: string): string {
  return `/api/v1/monitors/${encodeURIComponent(monitorID)}/report.pdf?month=${encodeURIComponent(month)}`
}

export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString()
}

/** A date and time short enough for a table column: "Nov 21, 7:13:37 PM", with the year only when it isn't the current one. */
export function formatShortDateTime(iso: string, now: Date = new Date()): string {
  const d = new Date(iso)
  return d.toLocaleString(undefined, {
    month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit',
    year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric',
  })
}

/** How long ago something happened, short enough for a table cell: "just now", "3 minutes ago", "2 hours ago", "4 days ago". */
export function timeAgo(iso: string, now: number = Date.now()): string {
  const seconds = Math.floor((now - Date.parse(iso)) / 1000)
  if (!Number.isFinite(seconds) || seconds < 45) return 'just now' // a few seconds ahead (clock skew) also counts as now
  const plural = (n: number, unit: string) => `${n} ${unit}${n === 1 ? '' : 's'} ago`
  const minutes = Math.max(1, Math.floor(seconds / 60))
  if (minutes < 60) return plural(minutes, 'minute')
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return plural(hours, 'hour')
  return plural(Math.floor(hours / 24), 'day')
}

/**
 * A plan price in US dollars from cents, shown exactly: "$0", "$19", "$19.99". Whole-dollar prices drop the cents; anything else keeps both
 * decimals, so a $19.99 plan is never displayed as $20.
 */
export function formatMoney(cents: number): string {
  const whole = cents % 100 === 0
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: whole ? 0 : 2, maximumFractionDigits: 2 }).format(cents / 100)
}

/** The visitor's own time zone, the default for new quiet hours. */
export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

/** "42 days", "Under a day", "Expired" for a domain's remaining time. */
export function domainLeft(expiresAt: string, now = Date.now()): string {
  const left = new Date(expiresAt).getTime() - now
  if (left <= 0) return 'Expired'
  const days = Math.floor(left / DAY)
  return days >= 1 ? `${days} day${days === 1 ? '' : 's'}` : 'Under a day'
}

const DAY = 86_400_000


export function certTone(msLeft: number): 'ok' | 'soon' | 'expired' {
  return msLeft <= 0 ? 'expired' : msLeft <= 30 * DAY ? 'soon' : 'ok'
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

export const DURATION_UNITS = [
  { value: 86400, label: 'days' },
  { value: 3600, label: 'hours' },
  { value: 60, label: 'minutes' },
] as const

/** The largest unit that divides a duration evenly, so 86400 is shown as 1 day rather than 1440 minutes. */
export function durationUnitFor(seconds: number): number {
  return DURATION_UNITS.find((u) => seconds % u.value === 0)?.value ?? 60
}

/** A duration in words: "30 seconds", "5 minutes", "1 hour", "2 days". */
export function formatPeriod(seconds: number): string {
  if (seconds < 60) return `${seconds} second${seconds === 1 ? '' : 's'}`
  const unit = durationUnitFor(seconds)
  const n = Math.round(seconds / unit)
  const word = unit === 86400 ? 'day' : unit === 3600 ? 'hour' : 'minute'
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

/** A check interval short enough for one line: "30 sec", "10 min", "1 hour", "1 day". */
export function shortInterval(seconds: number): string {
  if (seconds < 60) return `${seconds} sec`
  return seconds < 3600 ? `${Math.round(seconds / 60)} min` : formatPeriod(seconds)
}

/**
 * One check's breakdown as a line of text, for the check history: "DNS 12 ms · connect 31 ms · TLS 48 ms · server 410 ms · 1 redirect". Inside each
 * part the spaces don't break (U+00A0), so a narrow column wraps only between parts, never between a number and its "ms".
 */
export function timingsLine(t: Timings): string {
  const part = (label: string, ms: number) => `${label}\u00a0${ms.toLocaleString()}\u00a0ms`
  const bits = [part('DNS', t.dns_ms), part('connect', t.connect_ms)]
  if (t.tls_ms > 0) bits.push(part('TLS', t.tls_ms))
  bits.push(part('server', t.wait_ms))
  if (t.redirects > 0) bits.push(`${t.redirects}\u00a0redirect${t.redirects === 1 ? '' : 's'}`)
  return bits.join(' · ')
}
