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

export const HTTP_METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'] as const
export type HttpMethod = (typeof HTTP_METHODS)[number]

/** Methods that may send a request body. */
export const BODY_METHODS: readonly HttpMethod[] = ['POST', 'PUT', 'PATCH', 'DELETE']

/**
 * Methods that change state on the target. The request is repeated on every check, so the server requires a verified email address and a
 * slower interval for them (monitors.MinWriteIntervalSeconds = 300 s).
 */
export const WRITE_METHODS: readonly HttpMethod[] = BODY_METHODS
export const WRITE_MIN_MINUTES = 5

export interface RequestHeader {
  name: string
  value: string
}

export type AuthType = 'none' | 'basic' | 'bearer' | 'header'

/** A monitor's authentication as the API returns it: the secret itself is never sent back. */
export interface MonitorAuth {
  type: AuthType
  username?: string
  header_name?: string
  secret_set: boolean
}

/** Sets authentication. Omit `secret` (or leave it empty) to keep the stored one when the type is unchanged. */
export interface MonitorAuthInput {
  type: AuthType
  username?: string
  header_name?: string
  secret?: string
}

export type JsonOperator = 'equals' | 'not_equals' | 'contains' | 'exists' | 'not_exists'

export interface Assertion {
  type: 'body_contains' | 'body_not_contains' | 'json_path'
  path?: string
  operator?: JsonOperator
  value?: string
}

export interface Monitor {
  id: string
  name: string
  url: string
  method: HttpMethod
  expected_status_code: number | null
  headers: RequestHeader[]
  body: string | null
  auth: MonitorAuth
  assertions: Assertion[]
  check_interval_seconds: number
  effective_check_interval_seconds: number
  timeout_seconds: number
  enabled: boolean
  current_status: MonitorStatus
  last_checked_at: string | null
  /** The HTTPS certificate as last seen by a check; null until the first one (and for http:// monitors). */
  ssl: SSLInfo | null
}

export interface SSLInfo {
  expires_at: string
  checked_at: string
  /** Whole days left; negative once expired. */
  days_remaining: number
  status: 'valid' | 'expiring' | 'expired'
}

export interface CertificateAlert {
  monitor_id: string
  monitor_name: string
  expires_at: string
  days_remaining: number
  status: 'expiring' | 'expired'
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
  certificates: CertificateAlert[]
  certificates_tracked: number
  generated_at: string
}

export interface MonitorInput {
  name: string
  url: string
  method: HttpMethod
  expected_status_code: number | null
  check_interval_seconds?: number
  headers?: RequestHeader[]
  body?: string | null
  auth?: MonitorAuthInput
  assertions?: Assertion[]
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
