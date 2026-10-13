export interface User {
  id: string
  email: string
  email_verified_at: string | null
}

export interface Organization {
  id: string
  name: string
  role: 'owner' | 'member'
}

export interface Plan {
  code: string
  name: string
  monthly_price_cents: number
  max_monitors: number
  minimum_check_interval_seconds: number
  history_retention_days: number
}

export interface Usage {
  plan: Plan
  monitors_used: number
  monitor_limit: number
  over_limit: boolean
  next_plan: Plan | null
}

export type MonitorStatus = 'unknown' | 'up' | 'down'

export interface Monitor {
  id: string
  name: string
  url: string
  method: 'GET' | 'HEAD'
  expected_status_code: number | null
  check_interval_seconds: number
  effective_check_interval_seconds: number
  timeout_seconds: number
  enabled: boolean
  current_status: MonitorStatus
  last_checked_at: string | null
}

export interface NotificationSetting {
  id: string
  user_id: string | null
  channel: 'email'
  destination: string
  enabled: boolean
  verified: boolean
  created_at: string
}

export interface Incident {
  id: string
  monitor_id: string
  monitor_name: string
  started_at: string
  resolved_at: string | null
  status: 'open' | 'resolved'
  cause_category: string | null
  summary: string | null
  duration_seconds: number
}

export interface BillingSubscription {
  billing_enabled: boolean
  plan: Plan
  status: 'active' | 'trialing' | 'past_due' | 'canceled' | 'incomplete'
  paid_access: boolean
  cancel_at_period_end: boolean
  current_period_end: string | null
  can_manage: boolean
  has_subscription: boolean
}

export interface RangeInfo {
  from: string
  to: string
  preset: string
  clamped_to_retention: boolean
  retention_days: number
}

export interface Uptime {
  from: string
  to: string
  clamped_to_retention: boolean
  total_checks: number
  successful_checks: number
  failed_checks: number
  uptime_percent: number | null
  insufficient_data: boolean
  min_checks: number
  avg_response_ms: number | null
  method: 'check_based'
}

export interface SeriesPoint {
  t: string
  avg_ms: number | null
  max_ms: number | null
  checks: number
  failed: number
}

export interface CheckResult {
  id: number
  checked_at: string
  success: boolean
  http_status_code: number | null
  response_time_ms: number | null
  error_category: string | null
  error_summary: string | null
}

export interface DashboardSummary {
  monitors: { total: number; up: number; down: number; pending: number; paused: number }
  open_incidents: number
  uptime: Uptime
  generated_at: string
}

export interface MonitorInput {
  name: string
  url: string
  method: 'GET' | 'HEAD'
  expected_status_code: number | null
  check_interval_seconds?: number
}

/** An error response from the API, using the common `{error: {code, message, details}}` envelope. */
export class ApiError extends Error {
  status: number
  code: string
  details: Record<string, unknown>

  constructor(status: number, code: string, message: string, details: Record<string, unknown> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }

  /** Field-level validation messages, if any. */
  get fields(): Record<string, string> {
    const f = this.details.fields
    return f && typeof f === 'object' ? (f as Record<string, string>) : {}
  }
}

const UNSAFE = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

function csrfCookie(): string | undefined {
  return document.cookie
    .split('; ')
    .find((c) => c.startsWith('csrf_token='))
    ?.slice('csrf_token='.length)
}

/** Calls the JSON API. Unsafe methods echo the CSRF cookie in a header, fetching one first if needed. */
export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (UNSAFE.has(method)) {
    if (!csrfCookie()) await fetch('/api/v1/auth/me', { credentials: 'same-origin' })
    const token = csrfCookie()
    if (token) headers['X-CSRF-Token'] = decodeURIComponent(token)
  }
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  let data: any = null
  try {
    data = await res.json()
  } catch {
    // non-JSON body; handled below
  }
  if (!res.ok) {
    const e = data?.error
    throw new ApiError(res.status, e?.code ?? 'UNKNOWN', e?.message ?? 'Something went wrong.', e?.details)
  }
  return data as T
}

/** Asks the backend whether it and its dependencies are ready. Never throws. */
export async function fetchApiStatus(signal?: AbortSignal): Promise<'ok' | 'unavailable'> {
  try {
    const res = await fetch('/readyz', { signal })
    return res.ok ? 'ok' : 'unavailable'
  } catch {
    return 'unavailable'
  }
}
