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

/** Seconds of [from, to) covered by outages (an open outage lasts until `to`). */
export function downSeconds(outages: Outage[], from: number, to: number): number {
  let total = 0
  for (const o of outages) {
    const start = Math.max(o.started_at, from)
    const end = Math.min(o.ended_at ?? to, to)
    if (end > start) total += end - start
  }
  return total
}

/** Uptime over [from, to) as a fraction, counting only time since measuring began; null when nothing of the period has been measured. */
export function uptime(outages: Outage[], from: number, to: number, measuredSince: number): number | null {
  const start = Math.max(from, measuredSince)
  if (to <= start) return null
  return 1 - downSeconds(outages, start, to) / (to - start)
}

export type DayStatus = 'none' | 'up' | 'minor' | 'major'

export interface Day {
  start: number
  status: DayStatus
  downSeconds: number
}

/** One bar per UTC day, oldest first, ending with today. A day with any downtime is minor; an hour or more is major. */
export function days(outages: Outage[], count: number, now: number, measuredSince: number | null): Day[] {
  const today = Math.floor(now / 86_400) * 86_400
  const out: Day[] = []
  for (let i = count - 1; i >= 0; i--) {
    const start = today - i * 86_400
    const end = Math.min(start + 86_400, now)
    if (measuredSince === null || end <= measuredSince) {
      out.push({ start, status: 'none', downSeconds: 0 })
      continue
    }
    const down = downSeconds(outages, Math.max(start, measuredSince), end)
    out.push({ start, status: down === 0 ? 'up' : down >= 3600 ? 'major' : 'minor', downSeconds: down })
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
