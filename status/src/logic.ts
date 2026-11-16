// The parts of the status page that are plain arithmetic: when a component counts as down, and how much of a period it was up. No Cloudflare
// APIs here, so the tests run anywhere.

/** What the page shows, in order. The engine has no address to check: HawkEye's worker sends a heartbeat instead (src/index.ts). */
export const COMPONENTS = [
  { id: 'website', name: 'Website and app', description: 'hawkeye.observer and the HawkEye app' },
  { id: 'api', name: 'API', description: 'The REST API and the MCP server for Claude' },
  { id: 'engine', name: 'Monitoring and alerts', description: 'Checks run on schedule, and outages are alerted' },
] as const

export type ComponentId = (typeof COMPONENTS)[number]['id']

/** A component is down after this many failed checks in a row (one a minute), counted from the first of them, so one slow answer is not an outage. */
export const FAILURES_TO_CONFIRM = 2
/** The engine is down when its newest heartbeat is older than this, or when it reports checks overdue by more than ENGINE_MAX_OVERDUE. */
export const HEARTBEAT_MAX_AGE = 3 * 60
export const ENGINE_MAX_OVERDUE = 5 * 60
/** A check slower than this fails. */
export const CHECK_TIMEOUT_MS = 10_000
/** Raw check results are kept this long. */
export const CHECKS_KEPT = 14 * 86_400

export interface State {
  failures: number
  firstFailureAt: number | null
  downSince: number | null
}

export interface Step {
  state: State
  /** Set when this result confirms an outage: it started at the first failure. */
  opened?: number
  /** Set when this result ends an outage. */
  closed?: number
}

/** Applies one check result taken at `now` to a component's state. */
export function step(s: State, ok: boolean, now: number): Step {
  if (ok) {
    const out: Step = { state: { failures: 0, firstFailureAt: null, downSince: null } }
    if (s.downSince !== null) out.closed = now
    return out
  }
  const firstFailureAt = s.firstFailureAt ?? now
  const failures = s.failures + 1
  if (s.downSince === null && failures >= FAILURES_TO_CONFIRM) {
    return { state: { failures, firstFailureAt, downSince: firstFailureAt }, opened: firstFailureAt }
  }
  return { state: { failures, firstFailureAt, downSince: s.downSince } }
}

/** Whether the engine's newest heartbeat says checks are running on time. */
export function engineOK(hb: { at: number; overdue_seconds: number } | null, now: number): { ok: boolean; detail: string } {
  if (!hb) return { ok: false, detail: 'no heartbeat yet' }
  const age = now - hb.at
  if (age > HEARTBEAT_MAX_AGE) return { ok: false, detail: `last heartbeat ${age}s ago` }
  if (hb.overdue_seconds > ENGINE_MAX_OVERDUE) return { ok: false, detail: `checks ${hb.overdue_seconds}s overdue` }
  return { ok: true, detail: '' }
}

export interface Outage {
  started_at: number
  ended_at: number | null
}

/**
 * A planned maintenance window, announced on the page before it starts (the API refuses one that starts in the past). Downtime inside it is shown
 * as maintenance and left out of uptime, both from the downtime and from the time measured. Downtime outside it, including an outage that runs
 * past its end, counts as usual. Outages themselves are always recorded as they happened: windows change only how they are counted.
 */
export interface Window {
  starts_at: number
  ends_at: number
}

type Span = [number, number]

/** The parts of [from, to) covered by the windows, merged and sorted. */
function covered(windows: Window[], from: number, to: number): Span[] {
  const spans = windows
    .map((w): Span => [Math.max(w.starts_at, from), Math.min(w.ends_at, to)])
    .filter(([a, b]) => b > a)
    .sort((x, y) => x[0] - y[0])
  const out: Span[] = []
  for (const [a, b] of spans) {
    const last = out[out.length - 1]
    if (last && a <= last[1]) last[1] = Math.max(last[1], b)
    else out.push([a, b])
  }
  return out
}

const length = (spans: Span[]) => spans.reduce((t, [a, b]) => t + (b - a), 0)

/** How much of [a, b) the spans cover. */
function overlap(a: number, b: number, spans: Span[]): number {
  let t = 0
  for (const [x, y] of spans) t += Math.max(0, Math.min(b, y) - Math.max(a, x))
  return t
}

/** Seconds of [from, to) covered by outages (an open outage lasts until `to`), leaving out what falls inside maintenance windows. */
export function downSeconds(outages: Outage[], from: number, to: number, windows: Window[] = []): number {
  return outageSeconds(outages, from, to, windows).counted
}

/** Seconds of [from, to) covered by outages, split into what counts as downtime and what fell inside maintenance windows. */
export function outageSeconds(outages: Outage[], from: number, to: number, windows: Window[] = []): { counted: number; maintenance: number } {
  const spans = covered(windows, from, to)
  let counted = 0, maintenance = 0
  for (const o of outages) {
    const start = Math.max(o.started_at, from)
    const end = Math.min(o.ended_at ?? to, to)
    if (end <= start) continue
    const inside = overlap(start, end, spans)
    maintenance += inside
    counted += end - start - inside
  }
  return { counted, maintenance }
}

/**
 * Uptime over [from, to) as a fraction, counting only time since measuring began and leaving maintenance windows out of the period; null when
 * nothing of the period has been measured (or all of it was maintenance).
 */
export function uptime(outages: Outage[], from: number, to: number, measuredSince: number, windows: Window[] = []): number | null {
  const start = Math.max(from, measuredSince)
  if (to <= start) return null
  const measured = to - start - length(covered(windows, start, to))
  if (measured <= 0) return null
  return 1 - downSeconds(outages, start, to, windows) / measured
}

/** The window in progress at `now`, if any. */
export function activeWindow<W extends Window>(windows: W[], now: number): W | undefined {
  return windows.find((w) => w.starts_at <= now && now < w.ends_at)
}

export type DayStatus = 'none' | 'up' | 'maintenance' | 'minor' | 'major'

export interface Day {
  start: number
  status: DayStatus
  downSeconds: number
  maintenanceSeconds: number
}

/**
 * One bar per UTC day, oldest first, ending with today. A day with any downtime is minor, an hour or more major; a day whose only downtime was
 * inside maintenance windows is shown as maintenance.
 */
export function days(outages: Outage[], count: number, now: number, measuredSince: number | null, windows: Window[] = []): Day[] {
  const today = Math.floor(now / 86_400) * 86_400
  const out: Day[] = []
  for (let i = count - 1; i >= 0; i--) {
    const start = today - i * 86_400
    const end = Math.min(start + 86_400, now)
    if (measuredSince === null || end <= measuredSince) {
      out.push({ start, status: 'none', downSeconds: 0, maintenanceSeconds: 0 })
      continue
    }
    const { counted: down, maintenance } = outageSeconds(outages, Math.max(start, measuredSince), end, windows)
    const status: DayStatus = down >= 3600 ? 'major' : down > 0 ? 'minor' : maintenance > 0 ? 'maintenance' : 'up'
    out.push({ start, status, downSeconds: down, maintenanceSeconds: maintenance })
  }
  return out
}

/** "99.98%": two decimals, rounded down, so any downtime at all keeps it below 100%. */
export function percent(f: number | null): string {
  if (f === null) return '–'
  return `${(Math.floor(f * 10_000) / 100).toFixed(2)}%`
}

/** "3 min", "1 h 20 min". */
export function duration(seconds: number): string {
  const m = Math.max(1, Math.round(seconds / 60))
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  return m % 60 ? `${h} h ${m % 60} min` : `${h} h`
}

export const INCIDENT_STATUSES = ['investigating', 'identified', 'monitoring', 'resolved'] as const
export type IncidentStatus = (typeof INCIDENT_STATUSES)[number]

// Limits on a maintenance window: it is announced, so it can't start in the past (a minute's grace for the request itself), and it is short.
export const MAINTENANCE_MAX_SECONDS = 4 * 3600
export const MAINTENANCE_MAX_AHEAD = 90 * 86_400

/**
 * An ISO 8601 date and time with a time zone: 2026-11-16T03:00Z, 2026-11-16T03:00:00.000+01:00. Every part has a fixed length, so matching takes
 * time in proportion to the input (no open-ended repetition for crafted input to make backtrack), and the input is length-checked first anyway.
 */
const ISO_WITH_ZONE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})$/

/** Reads a time given as Unix seconds or an ISO 8601 string with a time zone. */
function when(v: unknown): number | null {
  if (typeof v === 'number' && Number.isInteger(v) && v > 0) return v
  if (typeof v === 'string' && v.length <= 40 && ISO_WITH_ZONE.test(v)) {
    const ms = Date.parse(v)
    return Number.isFinite(ms) ? Math.floor(ms / 1000) : null
  }
  return null
}

export interface WindowInput {
  title: string
  message: string
  starts_at: number
  ends_at: number
}

/**
 * Checks a request to schedule maintenance: { title, message?, starts_at, ends_at | duration_minutes }, times as Unix seconds or ISO 8601 with a
 * time zone. Returns the window, or what is wrong with it.
 */
export function parseWindow(b: Record<string, unknown> | null, now: number): WindowInput | string {
  const title = typeof b?.title === 'string' ? b.title.trim() : ''
  if (title.length < 1 || title.length > 200) return 'expected title (up to 200 characters)'
  const message = typeof b?.message === 'string' ? b.message.trim() : ''
  if (message.length > 2000) return 'message is too long (up to 2000 characters)'
  const starts = when(b?.starts_at)
  if (starts === null) return 'expected starts_at: Unix seconds, or ISO 8601 with a time zone (2026-11-16T03:00:00Z)'
  const minutes = b?.duration_minutes
  const ends = b?.ends_at !== undefined ? when(b.ends_at)
    : typeof minutes === 'number' && Number.isInteger(minutes) && minutes > 0 ? starts + minutes * 60 : null
  if (ends === null) return 'expected ends_at (like starts_at) or duration_minutes'
  if (starts < now - 60) return 'starts_at is in the past: maintenance is announced before it starts, so it can never cover downtime that already happened'
  if (starts > now + MAINTENANCE_MAX_AHEAD) return 'starts_at is more than 90 days away'
  if (ends <= starts) return 'ends_at must be after starts_at'
  if (ends - starts > MAINTENANCE_MAX_SECONDS) return 'a maintenance window can last at most 4 hours'
  return { title, message, starts_at: Math.max(starts, now), ends_at: ends }
}
