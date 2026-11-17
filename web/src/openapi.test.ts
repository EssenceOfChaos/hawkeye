import { describe, expect, it } from 'vitest'
import { monitorFor, needsBase, paramKey, readSpec, ready, type Operation, type SpecSummary } from './openapi'

const ordersV3 = `
openapi: 3.1.0
info:
  title: Orders API
  version: '1.4'
servers:
  - url: https://{region}.api.example.com/v1
    variables:
      region: { default: eu }
  - url: /v1
security:
  - bearerAuth: []
paths:
  /orders:
    get:
      summary: List orders
      tags: [Orders]
      parameters:
        - { name: limit, in: query, required: true, schema: { type: integer, example: 5 } }
        - { name: cursor, in: query, schema: { type: string } }
        - { name: X-Tenant, in: header, required: true, example: acme }
        - { name: User-Agent, in: header, required: true, example: x }
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema: { $ref: '#/components/schemas/OrderList' }
    post:
      summary: Create an order
      tags: [Orders]
      requestBody:
        required: true
        content:
          application/json:
            example: { sku: A-1, qty: 2 }
      responses:
        '201': { description: created }
  /orders/{id}:
    parameters:
      - { name: id, in: path, required: true, schema: { type: string } }
    get:
      operationId: getOrder
      tags: [Orders]
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                allOf:
                  - $ref: '#/components/schemas/Base'
                  - type: object
                    required: [state, weird.name]
                    properties:
                      state: { type: string }
    delete:
      tags: [Orders]
      deprecated: true
      requestBody:
        required: true
        content: { application/json: { schema: { type: object } } }
      responses: { '204': { description: gone } }
    trace:
      responses: { '200': { description: x } }
  /health:
    get:
      security: []
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [status, version]
                properties:
                  status: { type: string, enum: [ok] }
                  version: { type: string }
  /reports:
    get:
      security: [{ queryKey: [] }]
      responses: { '200': { $ref: 'https://example.com/shared.yaml#/Report' } }
  /me:
    get:
      security: [{ oauth: [read] }, { bearerAuth: [] }]
      responses: { '2XX': { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
    queryKey: { type: apiKey, in: query, name: key }
    oauth: { type: oauth2, flows: {} }
  schemas:
    Base:
      type: object
      required: [id, kind]
      properties:
        id: { type: string }
        kind: { const: order }
    OrderList:
      type: object
      required: [items, total]
      properties:
        items: { type: array }
        total: { type: integer }
`

const petsV2 = JSON.stringify({
  swagger: '2.0',
  info: { title: 'Pets', version: '1' },
  host: 'pets.example.com',
  basePath: '/api',
  schemes: ['http', 'https'],
  produces: ['application/json'],
  securityDefinitions: { basic: { type: 'basic' }, key: { type: 'apiKey', in: 'header', name: 'X-API-Key' } },
  security: [{ key: [] }],
  paths: {
    '/pets': {
      get: { parameters: [{ name: 'species', in: 'query', required: true, type: 'string', enum: ['cat', 'dog'] }],
        responses: { 200: { description: 'ok', schema: { type: 'object', required: ['pets'], properties: { pets: { type: 'array' } } } } } },
      post: { security: [{ basic: [] }], parameters: [{ name: 'pet', in: 'body', required: true, schema: { example: { name: 'Rex' } } }],
        responses: { 201: { description: 'created' } } },
    },
  },
})

function spec(text: string): SpecSummary {
  const r = readSpec(text)
  if (!r.ok) throw new Error(r.error)
  return r.spec
}
const op = (s: SpecSummary, id: string): Operation => {
  const o = s.operations.find((x) => x.id === id)
  if (!o) throw new Error(`no ${id} in ${s.operations.map((x) => x.id).join(', ')}`)
  return o
}

describe('readSpec: OpenAPI 3', () => {
  const s = spec(ordersV3)

  it('reads the title, format and servers, filling in server variables', () => {
    expect(s).toMatchObject({ title: 'Orders API', format: 'OpenAPI 3.1.0', servers: ['https://eu.api.example.com/v1', '/v1'] })
  })

  it('lists health endpoints first and writes last, and skips TRACE with a note', () => {
    expect(s.operations[0].id).toBe('GET /health')
    expect(s.operations.slice(-2).map((o) => o.id)).toEqual(['POST /orders', 'DELETE /orders/{id}'])
    expect(s.operations.some((o) => o.path === '/orders/{id}' && o.method === ('TRACE' as never))).toBe(false)
    expect(s.notes.join(' ')).toMatch(/1 TRACE operation was skipped/)
  })

  it('checks a health endpoint’s status and required fields, with a fixed value where the spec has one', () => {
    expect(op(s, 'GET /health')).toMatchObject({
      health: true, credential: null, expectedStatus: 200, name: 'GET /health',
      assertions: [{ type: 'json_path', path: 'status', operator: 'equals', value: 'ok' }, { type: 'json_path', path: 'version', operator: 'exists' }],
    })
  })

  it('follows references and allOf, and skips names a JSON path can’t express', () => {
    const o = op(s, 'GET /orders/{id}')
    expect(o.name).toBe('getOrder')
    expect(o.assertions).toEqual([
      { type: 'json_path', path: 'id', operator: 'exists' },
      { type: 'json_path', path: 'kind', operator: 'equals', value: 'order' },
      { type: 'json_path', path: 'state', operator: 'exists' },
    ])
    expect(o.params).toEqual([{ name: 'id', in: 'path', value: '', description: undefined }])
  })

  it('keeps required query and header parameters with their examples, and drops headers HawkEye sets', () => {
    const o = op(s, 'GET /orders')
    expect(o.params.map((p) => [p.in, p.name, p.value])).toEqual([['query', 'limit', '5'], ['header', 'X-Tenant', 'acme']])
    expect(o.credential).toBe('bearerAuth')
    expect(o.assertions.map((a) => a.path)).toEqual(['items', 'total'])
  })

  it('uses a write’s example body, and asks for one when the spec requires a body without an example', () => {
    expect(op(s, 'POST /orders')).toMatchObject({ write: true, body: '{"sku":"A-1","qty":2}', needsBody: false, expectedStatus: 201 })
    expect(op(s, 'DELETE /orders/{id}')).toMatchObject({ write: true, body: '', needsBody: true, deprecated: true })
  })

  it('picks a supported alternative among the security options, and marks unsupported ones', () => {
    expect(op(s, 'GET /me')).toMatchObject({ credential: 'bearerAuth', expectedStatus: null })
    expect(op(s, 'GET /reports').credential).toBe('queryKey')
    expect(s.credentials.find((c) => c.key === 'queryKey')).toMatchObject({ kind: 'unsupported', label: 'API key in the query string' })
    expect(s.credentials.find((c) => c.key === 'oauth')).toMatchObject({ kind: 'unsupported', label: 'OAuth 2' })
    expect(s.notes.join(' ')).toMatch(/refer to other files/)
  })

  it('says what can be created as it stands', () => {
    expect(ready(op(s, 'GET /health'), {}, '', s.credentials)).toBe(true)
    const byID = op(s, 'GET /orders/{id}')
    expect(ready(byID, {}, '', s.credentials)).toBe(false)
    expect(ready(byID, { [paramKey(byID, byID.params[0])]: 'ord_1' }, '', s.credentials)).toBe(true)
    expect(ready(op(s, 'DELETE /orders/{id}'), { 'DELETE /orders/{id}|path|id': '1' }, '', s.credentials)).toBe(false)
    expect(ready(op(s, 'GET /reports'), {}, '', s.credentials)).toBe(false)
  })
})

describe('readSpec: Swagger 2', () => {
  const s = spec(petsV2)

  it('builds the address from host, basePath and the https scheme', () => {
    expect(s).toMatchObject({ title: 'Pets', format: 'Swagger 2.0', servers: ['https://pets.example.com/api'] })
  })

  it('reads parameters, body examples, response schemas and credentials', () => {
    expect(op(s, 'GET /pets')).toMatchObject({ credential: 'key', params: [{ name: 'species', in: 'query', value: 'cat' }],
      assertions: [{ type: 'json_path', path: 'pets', operator: 'exists' }] })
    expect(op(s, 'POST /pets')).toMatchObject({ credential: 'basic', body: '{"name":"Rex"}', needsBody: false })
    expect(s.credentials).toEqual([
      { key: 'basic', kind: 'basic', label: 'Username and password' },
      { key: 'key', kind: 'header', headerName: 'X-API-Key', label: 'API key in the X-API-Key header' },
    ])
  })
})

describe('readSpec: refusals', () => {
  it('explains what it can’t read', () => {
    const err = (t: string) => {
      const r = readSpec(t)
      return r.ok ? 'parsed' : r.error
    }
    expect(err('  ')).toMatch(/Choose a file/)
    expect(err('{"name": "x"}')).toMatch(/isn’t an OpenAPI 3 or Swagger 2 spec/)
    expect(err('openapi: 3.0.0\npaths: {}')).toMatch(/no operations/)
    expect(err('{ nope')).toMatch(/isn’t valid JSON or YAML/)
    expect(err('openapi: 3.0.0\n  bad: [')).toMatch(/isn’t valid JSON or YAML/)
    expect(err('x'.repeat(5 * 1024 * 1024 + 1))).toMatch(/larger than 5 MB/)
  })

  it('refuses a YAML alias bomb instead of expanding it', () => {
    const bomb = ['openapi: 3.0.0', 'a: &a ["x","x","x","x","x","x","x","x","x"]',
      ...'bcdefghij'.split('').map((c, i) => `${c}: &${c} [${Array(9).fill('*' + 'abcdefghij'[i]).join(',')}]`),
      'paths:', '  /health:', '    get:', "      responses: { '200': { description: ok } }"].join('\n')
    const r = readSpec(bomb)
    expect(r.ok ? 'parsed' : r.error).toMatch(/isn’t valid JSON or YAML: .*alias/i)
    // The same file without the aliases reads, so it is the alias limit that refused it.
    expect(readSpec(bomb.split('\n').filter((l) => !/^[a-j]:/.test(l)).join('\n')).ok).toBe(true)
  })

  it('survives a reference loop', () => {
    const loop = `openapi: 3.0.0
paths:
  /x:
    get:
      responses:
        '200':
          content:
            application/json:
              schema: { $ref: '#/components/schemas/A' }
components:
  schemas:
    A: { $ref: '#/components/schemas/B' }
    B: { $ref: '#/components/schemas/A' }`
    expect(readSpec(loop)).toMatchObject({ ok: true })
  })
})

describe('monitorFor', () => {
  const s = spec(ordersV3)

  it('builds the address from the base, the path values and required query parameters, and sends the credential', () => {
    const o = op(s, 'GET /orders')
    const m = monitorFor(o, 'https://api.example.com/v1/', {}, '', s.credentials, { bearerAuth: { secret: 'tok' } }, ['t1'])
    expect(m).toEqual({
      name: 'List orders', url: 'https://api.example.com/v1/orders?limit=5', method: 'GET', expected_status_code: 200,
      headers: [{ name: 'X-Tenant', value: 'acme' }], assertions: o.assertions, body: null, auth: { type: 'bearer', secret: 'tok' }, tag_ids: ['t1'],
    })
  })

  it('encodes path values and sends a write’s body', () => {
    const del = op(s, 'DELETE /orders/{id}')
    const m = monitorFor(del, 'https://api.example.com', { [paramKey(del, del.params[0])]: 'a/b c' }, '{"x":1}', s.credentials, {}, [])
    expect(m).toMatchObject({ url: 'https://api.example.com/orders/a%2Fb%20c', body: '{"x":1}', method: 'DELETE', auth: { type: 'none' } })
  })

  it('maps basic and header credentials', () => {
    const p = spec(petsV2)
    expect(monitorFor(op(p, 'POST /pets'), 'https://x.example', {}, '{}', p.credentials, { basic: { username: 'u', secret: 'p' } }, []).auth)
      .toEqual({ type: 'basic', username: 'u', secret: 'p' })
    expect(monitorFor(op(p, 'GET /pets'), 'https://x.example', {}, '', p.credentials, { key: { secret: 'k' } }, []).auth)
      .toEqual({ type: 'header', header_name: 'X-API-Key', secret: 'k' })
  })
})

describe('needsBase', () => {
  it('asks for an address when the spec’s is relative or still has a placeholder', () => {
    expect(needsBase('https://api.example.com/v1')).toBe(false)
    expect(needsBase('/v1')).toBe(true)
    expect(needsBase('https://{tenant}.example.com')).toBe(true)
    expect(needsBase('')).toBe(true)
  })
})
