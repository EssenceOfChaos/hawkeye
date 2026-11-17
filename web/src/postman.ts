import { HTTP_METHODS, type HttpMethod } from './api'
import { HEALTH, isObj, RESERVED, str, WRITES, type Obj } from './importShared'
import type { Credential, EnteredCredential, Operation, Param, SpecResult } from './openapi'

/**
 * Reads a Postman collection (v2.0 or v2.1, as Postman exports it) into the same checklist as an OpenAPI spec (openapi.ts), in the browser.
 *
 * - The collection's variables fill in {{name}} placeholders. A placeholder with no value becomes a value the person types: in the path or query
 *   it's asked for on the row; at the start of the address it becomes the API address (usually {{baseUrl}}, whose value lives in an environment).
 * - Auth comes from the request, else its folder, else the collection, as in Postman. Tokens the collection holds are filled in for the person to keep.
 * - A saved example response with a 2xx code sets the expected status. Examples aren't schemas, and test scripts aren't run, so there are no
 *   response checks beyond the status.
 */

const VAR = /\{\{\s*([^{}]+?)\s*\}\}/g
const hasVar = (s: string) => /\{\{[^{}]+\}\}/.test(s)

type Kind = 'collection' | 'collection-v1' | 'environment' | null

/** What kind of Postman file this is, if any. */
export function postmanKind(root: Obj): Kind {
  const info = isObj(root.info) ? root.info : undefined
  if ((str(info?.schema) ?? '').includes('getpostman.com/json/collection') || (info && Array.isArray(root.item) && str(info._postman_id))) return 'collection'
  if (Array.isArray(root.requests) && (Array.isArray(root.order) || Array.isArray(root.folders))) return 'collection-v1'
  if (root._postman_variable_scope !== undefined || (Array.isArray(root.values) && str(root.name) && !root.paths)) return 'environment'
  return null
}

/** Postman lists key/value pairs as [{key, value, disabled}]. */
function pairs(v: unknown): { key: string; value: string }[] {
  if (!Array.isArray(v)) return []
  return v.filter((p): p is Obj => isObj(p) && p.disabled !== true && typeof p.key === 'string')
    .map((p) => ({ key: p.key as string, value: typeof p.value === 'string' ? p.value : p.value == null ? '' : String(p.value) }))
}
const pick = (v: unknown, key: string) => pairs(v).find((p) => p.key === key)?.value

interface Auth {
  type: string
  values: Obj
}

export function readPostman(root: Obj): SpecResult {
  const info = isObj(root.info) ? root.info : {}
  const title = (str(info.name) ?? 'Postman collection').trim() || 'Postman collection'
  const version = /collection\/(v[\d.]+)/.exec(str(info.schema) ?? '')?.[1]?.replace(/\.0$/, '') ?? 'v2'
  const vars = new Map(pairs(root.variable).filter((p) => p.value !== '').map((p) => [p.key, p.value]))
  const subst = (s: string) => s.replace(VAR, (m, name: string) => vars.get(name) ?? m)

  const credentials: Credential[] = []
  /** The credential for an auth setting, shared by every request that has the same one. */
  const credentialFor = (kind: Credential['kind'], base: string, label: string, prefill: EnteredCredential | undefined, extra: Partial<Credential> = {}): string => {
    const same = credentials.find((c) => c.kind === kind && c.headerName === extra.headerName && c.label === label &&
      (c.prefill?.secret ?? '') === (prefill?.secret ?? '') && (c.prefill?.username ?? '') === (prefill?.username ?? ''))
    if (same) return same.key
    let key = base
    for (let n = 2; credentials.some((c) => c.key === key); n++) key = `${base} ${n}`
    credentials.push({ key, kind, label, prefill, ...extra })
    return key
  }
  /** A value from the collection, or undefined while it still holds a placeholder with no value. */
  const known = (v: string | undefined) => {
    if (v === undefined) return undefined
    const s = subst(v)
    return hasVar(s) || s === '' ? undefined : s
  }

  const authCredential = (auth: Auth | undefined): string | null => {
    if (!auth || auth.type === 'noauth') return null
    const v = auth.values[auth.type]
    switch (auth.type) {
      case 'bearer': {
        const token = known(pick(v, 'token'))
        return credentialFor('bearer', 'Bearer token', 'Bearer token', token ? { secret: token } : undefined)
      }
      case 'basic': {
        const username = known(pick(v, 'username'))
        const password = known(pick(v, 'password'))
        return credentialFor('basic', 'Basic', 'Username and password', username || password ? { username: username ?? '', secret: password ?? '' } : undefined)
      }
      case 'apikey': {
        const where = pick(v, 'in') ?? 'header'
        const name = subst(pick(v, 'key') ?? 'X-API-Key')
        const value = known(pick(v, 'value'))
        if (where !== 'header') {
          return credentialFor('unsupported', 'API key', `API key in the query string`, undefined,
            { why: 'HawkEye stores keys encrypted only in a header or credentials, not in the address. If your API also accepts the key in a header, add it as one.' })
        }
        return credentialFor('header', name, `API key in the ${name} header`, value ? { secret: value } : undefined, { headerName: name })
      }
      default:
        return credentialFor('unsupported', auth.type, auth.type === 'oauth2' ? 'OAuth 2' : `Postman ${auth.type} auth`, undefined, {
          why: auth.type === 'oauth2'
            ? 'OAuth sign-in isn’t filled in automatically. Use a multi-step API check whose first step gets a token, then pass it to the next step.'
            : 'HawkEye can’t sign requests this way.',
        })
    }
  }
  const authOf = (o: Obj): Auth | undefined | 'inherit' => {
    if (!isObj(o.auth)) return 'inherit'
    const type = str(o.auth.type)
    if (!type || type === 'inherit') return 'inherit'
    return { type, values: o.auth }
  }

  interface Draft extends Omit<Operation, 'id' | 'path'> {
    origin: string
    rest: string
  }
  const drafts: Draft[] = []
  let skippedMethods = 0
  let dynamic = false

  const walk = (items: unknown, group: string | null, inherited: Auth | undefined) => {
    if (!Array.isArray(items)) return
    for (const it of items) {
      if (!isObj(it)) continue
      const own = authOf(it)
      const auth = own === 'inherit' ? inherited : own
      if (Array.isArray(it.item)) {
        walk(it.item, group ?? (str(it.name) || 'Requests'), auth)
        continue
      }
      const req = typeof it.request === 'string' ? { url: it.request } : it.request
      if (!isObj(req)) continue
      const reqAuth = authOf(req)
      const effective = reqAuth === 'inherit' ? auth : reqAuth
      const method = (str(req.method) ?? 'GET').toUpperCase() as HttpMethod
      if (!HTTP_METHODS.includes(method)) {
        skippedMethods++
        continue
      }

      // The address: raw text, or Postman's parts.
      const u = req.url
      let raw = typeof u === 'string' ? u : isObj(u) ? str(u.raw) ?? '' : ''
      if (raw === '' && isObj(u)) {
        const host = Array.isArray(u.host) ? u.host.join('.') : str(u.host) ?? ''
        const path = Array.isArray(u.path) ? u.path.join('/') : str(u.path) ?? ''
        raw = `${str(u.protocol) ? `${str(u.protocol)}://` : ''}${host}/${path}`
      }
      raw = subst(raw.trim()).replace(/#.*$/, '')
      if (/\{\{\s*\$/.test(raw)) dynamic = true
      let origin: string
      let rest: string
      const abs = /^(https?:\/\/[^/?#]+)(.*)$/i.exec(raw)
      const lead = /^(\{\{[^{}]+\}\})(.*)$/.exec(raw)
      if (abs) [, origin, rest] = abs
      else if (lead) [, origin, rest] = lead
      else {
        const bare = /^([^/?#]+)(.*)$/.exec(raw)
        if (!bare) continue
        origin = `https://${bare[1]}`
        rest = bare[2]
      }
      if (rest === '' || rest.startsWith('?')) rest = '/' + rest

      const params: Param[] = []
      const addParam = (p: Param) => {
        if (!params.some((x) => x.in === p.in && x.name === p.name)) params.push(p)
      }
      const pathVars = new Map(pairs(isObj(u) ? u.variable : undefined).map((p) => [p.key, subst(p.value)]))
      const [pathPart, queryPart = ''] = rest.split(/\?(.*)/s)
      const path = pathPart.split('/').map((seg) => {
        if (seg.startsWith(':') && seg.length > 1) {
          const name = seg.slice(1)
          const value = pathVars.get(name) ?? ''
          addParam({ name, in: 'path', value: hasVar(value) ? '' : value })
          return `{${name}}`
        }
        return seg.replace(VAR, (_, name: string) => {
          addParam({ name, in: 'path', value: '' })
          return `{${name}}`
        })
      }).join('/')

      // Query: literal pairs stay in the address; ones whose value is a placeholder with no value are asked for.
      const query = isObj(u) && Array.isArray(u.query) ? pairs(u.query) : queryPart.split('&').filter(Boolean).map((kv) => {
        const at = kv.indexOf('=')
        return at < 0 ? { key: kv, value: '' } : { key: kv.slice(0, at), value: kv.slice(at + 1) }
      })
      const literal: string[] = []
      for (const q of query) {
        const value = subst(q.value)
        if (hasVar(value)) addParam({ name: q.key, in: 'query', value: '' })
        else literal.push(value === '' ? q.key : `${q.key}=${value}`)
      }

      // Headers, and an Authorization header standing in for auth.
      let credential = authCredential(effective)
      const headerParams: Param[] = []
      for (const h of pairs(req.header)) {
        const name = h.key.trim()
        const value = subst(h.value)
        if (name.toLowerCase() === 'authorization' && !credential) {
          const [scheme, ...more] = value.split(/\s+/)
          const token = more.join(' ')
          const k = /^bearer$/i.test(scheme) && token ? credentialFor('bearer', 'Bearer token', 'Bearer token', hasVar(token) ? undefined : { secret: token })
            : credentialFor('header', 'Authorization', 'Authorization header value', hasVar(value) ? undefined : { secret: value }, { headerName: 'Authorization' })
          credential = k
          continue
        }
        if (RESERVED.has(name.toLowerCase())) continue
        headerParams.push({ name, in: 'header', value: hasVar(value) ? '' : value })
      }
      for (const h of headerParams) addParam(h)

      // Body, for writes only.
      const write = WRITES.has(method)
      let body = ''
      let blocked: string | undefined
      let hint: string | undefined
      if (write && isObj(req.body)) {
        const b = req.body
        switch (str(b.mode)) {
          case 'raw':
            body = subst(str(b.raw) ?? '')
            break
          case 'urlencoded':
            body = pairs(b.urlencoded).map((p) => `${encodeURIComponent(p.key)}=${encodeURIComponent(subst(p.value))}`).join('&')
            break
          case 'graphql': {
            const g = isObj(b.graphql) ? b.graphql : {}
            let variables: unknown
            try {
              variables = str(g.variables) ? JSON.parse(subst(str(g.variables)!)) : undefined
            } catch {
              variables = undefined
            }
            body = JSON.stringify(variables === undefined ? { query: subst(str(g.query) ?? '') } : { query: subst(str(g.query) ?? ''), variables })
            break
          }
          case 'formdata':
          case 'file':
            blocked = 'It sends a form upload or a file, which HawkEye can’t send.'
            break
        }
        if (hasVar(body)) hint = 'The body uses a variable with no value in the collection. Replace it before adding this check.'
      }

      // Expected status from saved examples.
      const codes = (Array.isArray(it.response) ? it.response : []).map((r) => (isObj(r) && typeof r.code === 'number' ? r.code : 0))
        .filter((c) => c >= 200 && c < 300).sort((a, b) => a - b)

      const finalPath = path + (literal.length > 0 ? `?${literal.join('&')}` : '')
      drafts.push({
        origin, rest: finalPath, method, name: (str(it.name)?.trim() || `${method} ${finalPath}`).replace(/\s+/g, ' ').slice(0, 100), group: group ?? 'Requests',
        params, body, needsBody: false, expectedStatus: codes[0] ?? null, assertions: [], credential, write, health: HEALTH.test(path),
        deprecated: false, blocked, hint,
      })
    }
  }
  const rootAuth = authOf(root)
  walk(root.item, null, rootAuth === 'inherit' ? undefined : rootAuth)
  if (drafts.length === 0) return { ok: false, error: 'The collection has no requests HawkEye can check.' }

  // One host: it becomes the API address and paths are added to it. Several: each check keeps its full address.
  const origins = [...new Set(drafts.map((d) => d.origin))]
  const single = origins.length === 1
  const seen = new Map<string, number>()
  const operations: Operation[] = drafts.map(({ origin, rest, ...d }) => {
    const path = single ? rest : origin + rest
    const base = `${d.method} ${path}`
    const n = (seen.get(base) ?? 0) + 1
    seen.set(base, n)
    const op: Operation = { ...d, id: n === 1 ? base : `${base} #${n}`, path }
    if (!single) {
      op.absolute = true
      if (hasVar(origin)) op.blocked ??= `Its address starts with ${origin}, which has no value in the collection.`
    }
    return op
  })
  const notes: string[] = ['Saved example responses set the expected status. Postman test scripts aren’t used, so each check tests the status only; add response checks afterward if you want them.']
  if (skippedMethods > 0) notes.push(`${skippedMethods} request${skippedMethods === 1 ? '' : 's'} with methods HawkEye can’t send ${skippedMethods === 1 ? 'was' : 'were'} skipped.`)
  if (dynamic) notes.push('Some addresses use Postman’s dynamic variables (such as {{$guid}}), which are only filled in inside Postman. Type a value for them.')
  const rank = (o: Operation) => (o.health ? 0 : o.write ? 2 : 1)
  operations.sort((a, b) => rank(a) - rank(b))
  return { ok: true, spec: { title, format: `Postman collection ${version}`, servers: single ? [origins[0]] : [], credentials, operations, notes } }
}
