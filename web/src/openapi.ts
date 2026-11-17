import { parse as parseYAML } from 'yaml'
import type { Assertion, HttpMethod, MonitorInput, RequestHeader } from './api'
import { asText, HEALTH, isObj, METHODS, RESERVED, str, WRITES, type Obj } from './importShared'
import { postmanKind, readPostman } from './postman'

/**
 * Reads an OpenAPI 3.x or Swagger 2.0 file (or a Postman collection, through postman.ts) into the API checks it describes, entirely in the browser
 * (docs/plan-api-import.md). Nothing here fetches anything: references to other files or URLs are not followed, only those inside the file.
 *
 * A spec describes a response's shape, not what a healthy one contains, so the checks it produces test the status code and that required fields
 * are present (and equal a value only where the spec fixes one). Customers can add more afterward.
 */

/** Larger files are refused before parsing. Big public specs (Stripe's, GitHub's) are a few MB. */
export const MAX_SPEC_BYTES = 5 * 1024 * 1024

export type CredentialKind = 'bearer' | 'basic' | 'header' | 'unsupported'

/** One way the API takes credentials, from securitySchemes (3.x) or securityDefinitions (2.0). */
export interface Credential {
  /** The scheme's name in the spec. */
  key: string
  kind: CredentialKind
  /** For kind header: the header the key goes in. */
  headerName?: string
  /** How to describe it to a person: "Bearer token", "API key in X-API-Key". */
  label: string
  /** For unsupported kinds: why, and what to do instead. */
  why?: string
  /** A value the file already holds (a Postman collection's token), filled in for the person to keep or change. */
  prefill?: EnteredCredential
}

export interface Param {
  name: string
  in: 'path' | 'query' | 'header'
  /** From the spec's example, default or first allowed value; '' when the person must supply one. */
  value: string
  description?: string
}

export interface Operation {
  /** "GET /users/{id}": unique within the spec. */
  id: string
  method: HttpMethod
  path: string
  /** For the monitor: the summary, else the operationId, else the method and path. */
  name: string
  /** The operation's first tag, for grouping. */
  group: string
  /** Path parameters, and the query and header parameters the spec marks required. */
  params: Param[]
  /** For POST, PUT, PATCH and DELETE: a JSON body from the spec's example, or '' when there is none. */
  body: string
  /** The spec requires a body and has no example, so the person must write one. */
  needsBody: boolean
  /** The lowest 2xx response the spec defines, or null (any response below 400 counts as up). */
  expectedStatus: number | null
  /** JSON path checks from the success response's schema. */
  assertions: Assertion[]
  /** The credential this operation uses (a key into SpecSummary.credentials), or null for none. */
  credential: string | null
  /** POST, PUT, PATCH or DELETE: repeated on every check, so never preselected. */
  write: boolean
  /** A health or status endpoint, which is offered first. */
  health: boolean
  deprecated: boolean
  /** path is a full address, used as it is rather than added to the base address (a Postman collection whose requests go to several hosts). */
  absolute?: boolean
  /** Why it can't be added (a form upload, an address with no value); it can't be ticked. */
  blocked?: string
  /** Something to check before adding it, such as a body that uses a variable with no value. */
  hint?: string
}

export interface SpecSummary {
  title: string
  /** "OpenAPI 3.1.0" or "Swagger 2.0". */
  format: string
  /** Base addresses from the spec, absolute or relative as written, with server variables filled in from their defaults. */
  servers: string[]
  credentials: Credential[]
  operations: Operation[]
  /** Things worth knowing about the file as a whole (references not followed, operations skipped). */
  notes: string[]
}

export type SpecResult = { ok: true; spec: SpecSummary } | { ok: false; error: string }

/** JSON path segments the checker reads as plain names (a.b.c); others would be read as a different path, so no check is made for them. */
const PLAIN_NAME = /^[A-Za-z_$][A-Za-z0-9_$-]*$/
const MAX_ASSERTIONS = 5

/** Reads a spec from text (JSON or YAML). */
export function readSpec(text: string): SpecResult {
  if (text.trim() === '') return { ok: false, error: 'Choose a file or paste a spec first.' }
  if (new Blob([text]).size > MAX_SPEC_BYTES) return { ok: false, error: 'The spec is larger than 5 MB. Remove the parts you don’t need to monitor, or split it.' }

  let root: unknown
  try {
    // maxAliasCount stops a file of nested YAML aliases from expanding into billions of nodes.
    root = /^\s*[{[]/.test(text) ? JSON.parse(text) : parseYAML(text, { maxAliasCount: 100, prettyErrors: false })
  } catch (e) {
    const where = e instanceof Error ? e.message.split('\n')[0] : ''
    return { ok: false, error: `The file isn’t valid JSON or YAML${where ? `: ${where}` : '.'}` }
  }
  if (!isObj(root)) return { ok: false, error: 'That isn’t an OpenAPI or Swagger spec, or a Postman collection.' }
  const postman = postmanKind(root)
  if (postman === 'collection') return readPostman(root)
  if (postman === 'collection-v1') {
    return { ok: false, error: 'This is a Postman collection in the old v1 format. In Postman, export it again as Collection v2.1 and import that.' }
  }
  if (postman === 'environment') {
    return { ok: false, error: 'This is a Postman environment, which holds variables rather than requests. Export the collection itself (Collection v2.1); you can type the base address on the next screen.' }
  }

  const v3 = str(root.openapi)
  const v2 = str(root.swagger) ?? (typeof root.swagger === 'number' ? String(root.swagger) : undefined)
  if (!(v3?.startsWith('3.') || v2?.startsWith('2'))) {
    return { ok: false, error: 'That isn’t an OpenAPI 3 or Swagger 2 spec (it has no "openapi: 3.x" or "swagger: 2.0" line at the top), or a Postman collection.' }
  }
  const isV3 = Boolean(v3?.startsWith('3.'))
  const notes: string[] = []
  let externalRefs = false

  /** Follows #/… references inside the file. External ones are left unresolved (and noted); a loop resolves to nothing. */
  const resolve = (node: unknown, depth = 0): unknown => {
    let cur = node
    for (let i = 0; isObj(cur) && typeof cur.$ref === 'string'; i++) {
      const ref = cur.$ref
      if (!ref.startsWith('#/')) {
        externalRefs = true
        return undefined
      }
      if (i > 20 || depth > 40) return undefined
      cur = ref.slice(2).split('/').reduce<unknown>((o, part) => {
        const key = decodeURIComponent(part).replace(/~1/g, '/').replace(/~0/g, '~')
        return isObj(o) ? o[key] : Array.isArray(o) ? o[Number(key)] : undefined
      }, root)
    }
    return cur
  }

  const info = isObj(root.info) ? root.info : {}
  const title = (str(info.title) ?? 'API').trim() || 'API'

  // Base addresses.
  const servers: string[] = []
  if (isV3) {
    for (const s of Array.isArray(root.servers) ? root.servers : []) {
      if (!isObj(s) || !str(s.url)) continue
      const vars = isObj(s.variables) ? s.variables : {}
      servers.push(str(s.url)!.replace(/\{([^}]+)\}/g, (m, name: string) => {
        const d = isObj(vars[name]) ? asText((vars[name] as Obj).default) : undefined
        return d ?? m
      }))
    }
  } else {
    const host = str(root.host)
    const basePath = str(root.basePath) ?? ''
    const schemes = Array.isArray(root.schemes) ? root.schemes.filter((x): x is string => typeof x === 'string') : []
    const scheme = schemes.includes('https') ? 'https' : schemes.includes('http') ? 'http' : 'https'
    servers.push(host ? `${scheme}://${host}${basePath}` : basePath || '/')
  }

  // Credentials.
  const credentials: Credential[] = []
  const schemesObj = resolve(isV3 ? (isObj(root.components) ? root.components.securitySchemes : undefined) : root.securityDefinitions)
  for (const [key, raw] of Object.entries(isObj(schemesObj) ? schemesObj : {})) {
    const s = resolve(raw)
    if (!isObj(s)) continue
    const type = str(s.type)?.toLowerCase()
    const scheme = str(s.scheme)?.toLowerCase()
    const where = str(s.in)
    const oauthWhy = 'OAuth sign-in isn’t filled in automatically. Use a multi-step API check whose first step gets a token, then pass it to the next step.'
    if (type === 'http' && scheme === 'bearer') credentials.push({ key, kind: 'bearer', label: 'Bearer token' })
    else if ((type === 'http' && scheme === 'basic') || type === 'basic') credentials.push({ key, kind: 'basic', label: 'Username and password' })
    else if (type === 'apikey' && where === 'header' && str(s.name)) {
      const name = str(s.name)!
      if (name.toLowerCase() === 'authorization') credentials.push({ key, kind: 'header', headerName: 'Authorization', label: 'Authorization header value' })
      else credentials.push({ key, kind: 'header', headerName: name, label: `API key in the ${name} header` })
    } else if (type === 'apikey') {
      credentials.push({ key, kind: 'unsupported', label: `API key in the ${where === 'query' ? 'query string' : where ?? 'request'}`,
        why: 'HawkEye stores keys encrypted only in a header or credentials, not in the address or a cookie. If your API also accepts the key in a header, add it as one.' })
    } else if (type === 'oauth2' || type === 'openidconnect') credentials.push({ key, kind: 'unsupported', label: type === 'oauth2' ? 'OAuth 2' : 'OpenID Connect', why: oauthWhy })
    else credentials.push({ key, kind: 'unsupported', label: scheme ? `HTTP ${scheme}` : type ?? 'Unknown', why: 'HawkEye can’t sign requests this way.' })
  }
  const credentialByKey = new Map(credentials.map((c) => [c.key, c]))

  /** The credential an operation uses: the first alternative HawkEye supports, none when one alternative is "no credentials". */
  const pickCredential = (security: unknown): string | null => {
    if (!Array.isArray(security) || security.length === 0) return null
    let fallback: string | null = null
    for (const req of security) {
      if (!isObj(req)) continue
      const keys = Object.keys(req)
      if (keys.length === 0) return null // credentials are optional
      if (keys.length === 1 && credentialByKey.get(keys[0])?.kind !== 'unsupported' && credentialByKey.has(keys[0])) return keys[0]
      fallback ??= keys.find((k) => credentialByKey.has(k)) ?? null
    }
    return fallback
  }

  /** An example for a parameter or schema, in the places specs put one. */
  const exampleOf = (p: Obj): unknown => {
    if (p.example !== undefined) return p.example
    if (isObj(p.examples)) {
      const first = resolve(Object.values(p.examples)[0])
      if (isObj(first) && first.value !== undefined) return first.value
    }
    if (p['x-example'] !== undefined) return p['x-example']
    const schema = resolve(p.schema)
    const src = isObj(schema) ? schema : p
    if (src.example !== undefined) return src.example
    if (Array.isArray(src.examples) && src.examples.length > 0) return src.examples[0]
    if (src.default !== undefined) return src.default
    if (Array.isArray(src.enum) && src.enum.length > 0) return src.enum[0]
    return undefined
  }

  /** Required properties and fixed values of an object schema, following allOf. */
  const objectShape = (schema: unknown, depth = 0): { props: Obj; required: string[] } => {
    const s = resolve(schema)
    const out = { props: {} as Obj, required: [] as string[] }
    if (!isObj(s) || depth > 10) return out
    if (Array.isArray(s.allOf)) {
      for (const part of s.allOf) {
        const inner = objectShape(part, depth + 1)
        Object.assign(out.props, inner.props)
        out.required.push(...inner.required)
      }
    }
    if (isObj(s.properties)) Object.assign(out.props, s.properties)
    if (Array.isArray(s.required)) out.required.push(...s.required.filter((x): x is string => typeof x === 'string'))
    return out
  }

  const assertionsFor = (schema: unknown): Assertion[] => {
    const { props, required } = objectShape(schema)
    const out: Assertion[] = []
    for (const name of [...new Set(required)]) {
      if (out.length >= MAX_ASSERTIONS) break
      if (!PLAIN_NAME.test(name)) continue
      const p = resolve(props[name])
      const fixed = isObj(p) ? (p.const !== undefined ? p.const : Array.isArray(p.enum) && p.enum.length === 1 ? p.enum[0] : undefined) : undefined
      const text = asText(fixed)
      out.push(text !== undefined ? { type: 'json_path', path: name, operator: 'equals', value: text } : { type: 'json_path', path: name, operator: 'exists' })
    }
    return out
  }

  const isJSON = (mediaType: string) => /^application\/(.+\+)?json\b/i.test(mediaType) || mediaType === '*/*'

  // Operations.
  const operations: Operation[] = []
  const paths = resolve(root.paths)
  let skipped = 0
  const globalProduces = Array.isArray(root.produces) ? root.produces.filter((x): x is string => typeof x === 'string') : []
  for (const [path, rawItem] of Object.entries(isObj(paths) ? paths : {})) {
    const item = resolve(rawItem)
    if (!isObj(item) || !path.startsWith('/')) continue
    const shared = Array.isArray(item.parameters) ? item.parameters : []
    for (const [m, rawOp] of Object.entries(item)) {
      const method = m.toUpperCase() as HttpMethod
      if (!METHODS.includes(method)) {
        if (m.toLowerCase() === 'trace') skipped++
        continue
      }
      const op = resolve(rawOp)
      if (!isObj(op)) continue

      // Parameters: the operation's replace the path's of the same name and location.
      const byKey = new Map<string, Obj>()
      for (const raw of [...shared, ...(Array.isArray(op.parameters) ? op.parameters : [])]) {
        const p = resolve(raw)
        if (isObj(p) && str(p.name) && str(p.in)) byKey.set(`${p.in}:${p.name}`, p)
      }
      const params: Param[] = []
      let bodyParam: Obj | undefined
      for (const p of byKey.values()) {
        const where = str(p.in)
        const name = str(p.name)!
        if (where === 'body') {
          bodyParam = p
          continue
        }
        const needed = where === 'path' || p.required === true
        if (!needed || (where !== 'path' && where !== 'query' && where !== 'header')) continue
        if (where === 'header' && RESERVED.has(name.toLowerCase())) continue
        params.push({ name, in: where, value: asText(exampleOf(p)) ?? '', description: str(p.description) })
      }
      // Path parameters the spec forgot to declare still need a value.
      for (const [, name] of path.matchAll(/\{([^}]+)\}/g)) {
        if (!params.some((p) => p.in === 'path' && p.name === name)) params.push({ name, in: 'path', value: '' })
      }

      // Request body (writes only: HawkEye sends a body only with POST, PUT, PATCH and DELETE).
      const write = WRITES.has(method)
      let body = ''
      let needsBody = false
      if (write) {
        let required = false
        let example: unknown
        if (isV3) {
          const rb = resolve(op.requestBody)
          if (isObj(rb)) {
            required = rb.required === true
            const content = isObj(rb.content) ? rb.content : {}
            const media = Object.entries(content).find(([t]) => isJSON(t))
            const mt = media ? resolve(media[1]) : undefined
            if (isObj(mt)) example = exampleOf(mt)
          }
        } else if (bodyParam) {
          required = bodyParam.required === true
          example = exampleOf(bodyParam)
        }
        if (example !== undefined) body = typeof example === 'string' ? example : JSON.stringify(example)
        needsBody = required && body === ''
      }

      // Success response.
      const responses = resolve(op.responses)
      const codes = Object.keys(isObj(responses) ? responses : {})
      const success = codes.filter((c) => /^2\d\d$/.test(c)).sort()
      const expectedStatus = success.length > 0 ? Number(success[0]) : null
      let assertions: Assertion[] = []
      const okKey = success[0] ?? codes.find((c) => /^2xx$/i.test(c))
      const ok = okKey && isObj(responses) ? resolve(responses[okKey]) : undefined
      if (isObj(ok) && method !== 'HEAD') {
        if (isV3) {
          const content = isObj(ok.content) ? ok.content : {}
          const media = Object.entries(content).find(([t]) => isJSON(t))
          const mt = media ? resolve(media[1]) : undefined
          if (isObj(mt)) assertions = assertionsFor(mt.schema)
        } else {
          const produces = Array.isArray(op.produces) ? op.produces.filter((x): x is string => typeof x === 'string') : globalProduces
          if (produces.length === 0 || produces.some(isJSON)) assertions = assertionsFor(ok.schema)
        }
      }

      const tags = Array.isArray(op.tags) ? op.tags.filter((x): x is string => typeof x === 'string') : []
      const name = (str(op.summary)?.trim() || str(op.operationId)?.trim() || `${method} ${path}`).replace(/\s+/g, ' ').slice(0, 100)
      operations.push({
        id: `${method} ${path}`, method, path, name, group: tags[0] ?? 'Other', params, body, needsBody, expectedStatus, assertions,
        credential: pickCredential(op.security !== undefined ? op.security : root.security),
        write, health: HEALTH.test(path), deprecated: op.deprecated === true,
      })
    }
  }
  if (operations.length === 0) return { ok: false, error: 'The spec has no operations HawkEye can check (no paths with GET, POST or other methods).' }
  if (externalRefs) notes.push('Some parts refer to other files or addresses, which aren’t read, so a few checks may have fewer response checks.')
  if (skipped > 0) notes.push(`${skipped} TRACE operation${skipped === 1 ? ' was' : 's were'} skipped: HawkEye can’t send TRACE requests.`)

  // Health endpoints first, then reads, in the spec's order otherwise.
  const rank = (o: Operation) => (o.health ? 0 : o.write ? 2 : 1)
  operations.sort((a, b) => rank(a) - rank(b))
  return { ok: true, spec: { title, format: isV3 ? `OpenAPI ${v3}` : `Swagger ${v2}`, servers, credentials, operations, notes } }
}

/** Whether an operation can be created as it stands: every value filled in, a body when one is needed, and credentials HawkEye supports. */
export function ready(op: Operation, values: Record<string, string>, body: string, credentials: Credential[]): boolean {
  if (op.blocked) return false
  if (op.params.some((p) => (values[paramKey(op, p)] ?? p.value).trim() === '')) return false
  if (op.needsBody && body.trim() === '') return false
  if (op.credential && credentials.find((c) => c.key === op.credential)?.kind === 'unsupported') return false
  return true
}

/** The key a parameter's value is kept under in the import screen. */
export const paramKey = (op: Operation, p: Param) => `${op.id}|${p.in}|${p.name}`

/** Whether a base address needs the person to type the API's public address: it's relative, or a placeholder was left unfilled. */
export function needsBase(url: string): boolean {
  return !/^https?:\/\/[^/{}]+/i.test(url) || /\{[^}]+\}/.test(url)
}

export interface EnteredCredential {
  username?: string
  secret: string
}

/** The POST /monitors body for an operation. */
export function monitorFor(op: Operation, base: string, values: Record<string, string>, body: string, credentials: Credential[],
  entered: Record<string, EnteredCredential>, tagIDs: string[]): MonitorInput {
  const value = (p: Param) => values[paramKey(op, p)] ?? p.value
  let path = op.path
  for (const p of op.params.filter((x) => x.in === 'path')) path = path.split(`{${p.name}}`).join(encodeURIComponent(value(p)))
  const query = op.params.filter((p) => p.in === 'query').map((p) => `${encodeURIComponent(p.name)}=${encodeURIComponent(value(p))}`)
  let url = op.absolute ? path : base.replace(/\/+$/, '') + path
  if (query.length > 0) url += (url.includes('?') ? '&' : '?') + query.join('&')
  const headers: RequestHeader[] = op.params.filter((p) => p.in === 'header').map((p) => ({ name: p.name, value: value(p) }))

  const input: MonitorInput = {
    name: op.name, url, method: op.method, expected_status_code: op.expectedStatus, headers, assertions: op.method === 'HEAD' ? [] : op.assertions,
    body: op.write && body.trim() !== '' ? body : null, auth: { type: 'none' }, tag_ids: tagIDs,
  }
  const cred = op.credential ? credentials.find((c) => c.key === op.credential) : undefined
  const typed = op.credential ? entered[op.credential] : undefined
  if (cred && typed && typed.secret !== '') {
    if (cred.kind === 'bearer') input.auth = { type: 'bearer', secret: typed.secret }
    else if (cred.kind === 'basic') input.auth = { type: 'basic', username: typed.username ?? '', secret: typed.secret }
    else if (cred.kind === 'header') input.auth = { type: 'header', header_name: cred.headerName, secret: typed.secret }
  }
  return input
}
