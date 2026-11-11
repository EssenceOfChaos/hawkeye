// status.hawkeye.observer. Every minute (the cron trigger) it checks HawkEye's website and API from Cloudflare and reads the newest heartbeat from
// HawkEye's monitoring engine, and records confirmed outages. It serves the page, a JSON copy of it, and two authenticated endpoints: heartbeats
// from HawkEye's worker, and incident notes from us. Nothing here depends on HawkEye's server or database.

import {
  CHECK_TIMEOUT_MS, CHECKS_KEPT, COMPONENTS, engineOK, INCIDENT_STATUSES, step,
  type ComponentId, type IncidentStatus, type Outage, type State,
} from './logic'
import { renderPage, type Snapshot } from './page'

export interface Env {
  DB: D1Database
  SITE: string // https://hawkeye.observer
  HEARTBEAT_TOKEN: string
  ADMIN_TOKEN: string
}

const PRIMARY_REGION = 'primary'

// ---- Checks (the cron trigger)

interface Result { ok: boolean; ms: number | null; detail: string }

async function probe(url: string, accept: (res: Response) => Promise<boolean>): Promise<Result> {
  const started = Date.now()
  try {
    const res = await fetch(url, {
      signal: AbortSignal.timeout(CHECK_TIMEOUT_MS),
      headers: { 'User-Agent': 'HawkEye-Status/1.0 (+https://status.hawkeye.observer)' },
      cf: { cacheTtl: 0 },
    })
    const ms = Date.now() - started
    const good = res.status >= 200 && res.status < 300 && (await accept(res))
    return { ok: good, ms, detail: good ? '' : `HTTP ${res.status}` }
  } catch (e) {
    return { ok: false, ms: null, detail: e instanceof Error ? e.name : 'error' }
  }
}

async function checkAll(env: Env, now: number): Promise<Record<ComponentId, Result>> {
  const [website, api, hb] = await Promise.all([
    probe(`${env.SITE}/`, async (r) => (await r.text()).includes('HawkEye')),
    // A public API route that reads the database: the API process, its routing and PostgreSQL all have to work.
    probe(`${env.SITE}/api/v1/plans`, async (r) => Array.isArray(((await r.json()) as { plans?: unknown }).plans)),
    env.DB.prepare('SELECT at, overdue_seconds FROM heartbeats WHERE region = ?').bind(PRIMARY_REGION).first<{ at: number; overdue_seconds: number }>(),
  ])
  const engine = engineOK(hb, now)
  return { website, api, engine: { ok: engine.ok, ms: null, detail: engine.detail } }
}

export async function runChecks(env: Env, now: number): Promise<void> {
  const results = await checkAll(env, now)
  const rows = await env.DB.prepare('SELECT component, failures, first_failure_at, down_since FROM component_state').all<{
    component: string; failures: number; first_failure_at: number | null; down_since: number | null
  }>()
  const states = new Map(rows.results.map((r) => [r.component, { failures: r.failures, firstFailureAt: r.first_failure_at, downSince: r.down_since } as State]))

  const writes: D1PreparedStatement[] = [env.DB.prepare('INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)').bind('measured_since', now)]
  for (const c of COMPONENTS) {
    const r = results[c.id]
    const s = step(states.get(c.id) ?? { failures: 0, firstFailureAt: null, downSince: null }, r.ok, now)
    writes.push(
      env.DB.prepare('INSERT OR REPLACE INTO checks (component, at, ok, ms, detail) VALUES (?, ?, ?, ?, ?)').bind(c.id, now, r.ok ? 1 : 0, r.ms, r.detail || null),
      env.DB.prepare(`INSERT INTO component_state (component, failures, first_failure_at, down_since) VALUES (?, ?, ?, ?)
        ON CONFLICT (component) DO UPDATE SET failures = excluded.failures, first_failure_at = excluded.first_failure_at, down_since = excluded.down_since`)
        .bind(c.id, s.state.failures, s.state.firstFailureAt, s.state.downSince),
    )
    if (s.opened !== undefined) writes.push(env.DB.prepare('INSERT INTO outages (component, started_at) VALUES (?, ?)').bind(c.id, s.opened))
    if (s.closed !== undefined) {
      writes.push(env.DB.prepare('UPDATE outages SET ended_at = ? WHERE component = ? AND ended_at IS NULL').bind(s.closed, c.id))
    }
  }
  // Once an hour, drop raw results nobody needs any more (outages, which the uptime is computed from, are kept).
  if (now % 3600 < 60) writes.push(env.DB.prepare('DELETE FROM checks WHERE at < ?').bind(now - CHECKS_KEPT))
  await env.DB.batch(writes) // one transaction: a round of results is recorded whole or not at all
}

// ---- Reading the page's data

export async function snapshot(env: Env, now: number): Promise<Snapshot> {
  const since = now - 90 * 86_400
  const recent = now - 30 * 86_400 // incidents resolved within this are still listed
  const [meta, states, outages, incidents, updates] = await env.DB.batch([
    env.DB.prepare("SELECT value FROM meta WHERE key = 'measured_since'"),
    env.DB.prepare('SELECT component, down_since FROM component_state'),
    env.DB.prepare('SELECT component, started_at, ended_at FROM outages WHERE ended_at IS NULL OR ended_at >= ? ORDER BY started_at').bind(since),
    env.DB.prepare('SELECT id, title, created_at, resolved_at FROM incidents WHERE resolved_at IS NULL OR resolved_at >= ? ORDER BY created_at DESC').bind(recent),
    env.DB.prepare(`SELECT u.incident_id, u.status, u.message, u.created_at FROM incident_updates u JOIN incidents i ON i.id = u.incident_id
      WHERE i.resolved_at IS NULL OR i.resolved_at >= ? ORDER BY u.created_at DESC`).bind(recent),
  ])
  const measured = (meta.results[0] as { value: number } | undefined)?.value ?? null
  const down = new Map((states.results as { component: string; down_since: number | null }[]).map((r) => [r.component, r.down_since]))
  const byComponent = new Map<string, Outage[]>()
  for (const o of outages.results as (Outage & { component: string })[]) {
    byComponent.set(o.component, [...(byComponent.get(o.component) ?? []), { started_at: o.started_at, ended_at: o.ended_at }])
  }
  const notes = updates.results as { incident_id: number; status: IncidentStatus; message: string; created_at: number }[]
  return {
    now,
    measuredSince: measured,
    components: COMPONENTS.map((c) => ({ ...c, downSince: down.get(c.id) ?? null, outages: byComponent.get(c.id) ?? [] })),
    incidents: (incidents.results as { id: number; title: string; created_at: number; resolved_at: number | null }[]).map((i) => ({
      ...i, updates: notes.filter((u) => u.incident_id === i.id),
    })),
  }
}

// ---- Requests

/** Compares a bearer token with the expected secret in constant time. An unset secret never matches. */
function authorized(req: Request, secret: string | undefined): boolean {
  const got = req.headers.get('Authorization')?.match(/^Bearer (.+)$/)?.[1] ?? ''
  if (!secret) return false
  const a = new TextEncoder().encode(got)
  const b = new TextEncoder().encode(secret)
  if (a.byteLength !== b.byteLength) return false
  return crypto.subtle.timingSafeEqual(a, b)
}

const json = (body: unknown, status = 200, headers: HeadersInit = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json; charset=utf-8', ...headers } })

const bad = (message: string, status = 400) => json({ error: message }, status)

async function body(req: Request): Promise<Record<string, unknown> | null> {
  try {
    const v = await req.json()
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
  } catch {
    return null
  }
}

const text = (v: unknown, max: number) => (typeof v === 'string' && v.trim().length > 0 && v.trim().length <= max ? v.trim() : null)
const status = (v: unknown) => (INCIDENT_STATUSES.includes(v as IncidentStatus) ? (v as IncidentStatus) : null)

export async function handle(req: Request, env: Env, now: number): Promise<Response> {
  const url = new URL(req.url)
  const route = `${req.method} ${url.pathname.replace(/\/+$/, '') || '/'}`

  if (route === 'GET /' || route === 'HEAD /') {
    const html = renderPage(await snapshot(env, now))
    return new Response(req.method === 'HEAD' ? null : html, {
      headers: {
        'Content-Type': 'text/html; charset=utf-8',
        'Cache-Control': 'public, max-age=30',
        'Content-Security-Policy': "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
        'X-Content-Type-Options': 'nosniff',
        'Referrer-Policy': 'no-referrer',
      },
    })
  }
  if (route === 'GET /api/status.json') {
    return json(await snapshot(env, now), 200, { 'Cache-Control': 'public, max-age=30', 'Access-Control-Allow-Origin': '*' })
  }

  // HawkEye's worker: { region, overdue_seconds }. Only the primary region's heartbeat decides the engine's status.
  if (route === 'POST /heartbeat') {
    if (!authorized(req, env.HEARTBEAT_TOKEN)) return bad('unauthorized', 401)
    const b = await body(req)
    const region = text(b?.region, 40)
    const overdue = b?.overdue_seconds
    if (!region || typeof overdue !== 'number' || !Number.isFinite(overdue) || overdue < 0) return bad('expected region and overdue_seconds')
    await env.DB.prepare(`INSERT INTO heartbeats (region, at, overdue_seconds) VALUES (?, ?, ?)
      ON CONFLICT (region) DO UPDATE SET at = excluded.at, overdue_seconds = excluded.overdue_seconds`).bind(region, now, Math.round(overdue)).run()
    return new Response(null, { status: 204 })
  }

  // Incident notes, from us: start one ({ title, status, message }) or add an update to one ({ status, message }; "resolved" closes it).
  if (route === 'POST /api/incidents') {
    if (!authorized(req, env.ADMIN_TOKEN)) return bad('unauthorized', 401)
    const b = await body(req)
    const title = text(b?.title, 200)
    const st = status(b?.status ?? 'investigating')
    const message = text(b?.message, 5000)
    if (!title || !st || !message) return bad('expected title (up to 200 characters), message, and status: investigating, identified, monitoring or resolved')
    const created = await env.DB.prepare('INSERT INTO incidents (title, created_at, resolved_at) VALUES (?, ?, ?) RETURNING id')
      .bind(title, now, st === 'resolved' ? now : null).first<{ id: number }>()
    await env.DB.prepare('INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, ?, ?, ?)').bind(created!.id, st, message, now).run()
    return json({ id: created!.id }, 201)
  }
  const m = route.match(/^POST \/api\/incidents\/(\d+)\/updates$/)
  if (m) {
    if (!authorized(req, env.ADMIN_TOKEN)) return bad('unauthorized', 401)
    const id = Number(m[1])
    const b = await body(req)
    const st = status(b?.status)
    const message = text(b?.message, 5000)
    if (!st || !message) return bad('expected message, and status: investigating, identified, monitoring or resolved')
    const found = await env.DB.prepare('SELECT id FROM incidents WHERE id = ?').bind(id).first()
    if (!found) return bad('no such incident', 404)
    await env.DB.batch([
      env.DB.prepare('INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, ?, ?, ?)').bind(id, st, message, now),
      env.DB.prepare('UPDATE incidents SET resolved_at = ? WHERE id = ?').bind(st === 'resolved' ? now : null, id),
    ])
    return new Response(null, { status: 204 })
  }
  return bad('not found', 404)
}

const nowSeconds = () => Math.floor(Date.now() / 1000)

export default {
  fetch: (req: Request, env: Env) => handle(req, env, nowSeconds()),
  // The cron fires on the minute; results are filed under that minute.
  scheduled: async (ev: ScheduledController, env: Env, ctx: ExecutionContext) => {
    ctx.waitUntil(runChecks(env, Math.floor(ev.scheduledTime / 60_000) * 60))
  },
} satisfies ExportedHandler<Env>
