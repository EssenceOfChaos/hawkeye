import { describe, expect, it } from 'vitest'
import { monitorFor, paramKey, readSpec, ready, type Operation, type SpecSummary } from './openapi'

const collection = {
  info: { _postman_id: 'c1', name: 'Shop API', schema: 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json' },
  variable: [{ key: 'baseUrl', value: '' }, { key: 'apiVersion', value: 'v1' }, { key: 'token', value: '' }],
  auth: { type: 'bearer', bearer: [{ key: 'token', value: '{{token}}', type: 'string' }] },
  item: [
    {
      name: 'Orders',
      item: [
        {
          name: 'List orders',
          request: {
            method: 'GET',
            header: [{ key: 'Accept', value: 'application/json' }, { key: 'X-Debug', value: '1', disabled: true }, { key: 'User-Agent', value: 'PostmanRuntime' }],
            url: { raw: '{{baseUrl}}/{{apiVersion}}/orders?limit=10&cursor={{cursor}}', host: ['{{baseUrl}}'], path: ['{{apiVersion}}', 'orders'],
              query: [{ key: 'limit', value: '10' }, { key: 'cursor', value: '{{cursor}}' }, { key: 'debug', value: 'true', disabled: true }] },
          },
          response: [{ name: 'ok', code: 200 }, { name: 'not found', code: 404 }],
        },
        {
          name: 'Get order',
          request: { method: 'GET', url: { raw: '{{baseUrl}}/v1/orders/:id', variable: [{ key: 'id', value: 'ord_1' }] } },
        },
        {
          name: 'Create order',
          request: { method: 'POST', url: '{{baseUrl}}/v1/orders', body: { mode: 'raw', raw: '{"sku":"A-1","coupon":"{{coupon}}"}' } },
          response: [{ code: 201 }],
        },
        {
          name: 'Upload receipt',
          request: { method: 'POST', url: '{{baseUrl}}/v1/receipts', body: { mode: 'formdata', formdata: [{ key: 'file', type: 'file', src: 'a.pdf' }] } },
        },
        { name: 'Copy order', request: { method: 'COPY', url: '{{baseUrl}}/v1/orders/1' } },
      ],
    },
    {
      name: 'Public',
      auth: { type: 'noauth' },
      item: [
        { name: 'Health', request: { method: 'GET', url: '{{baseUrl}}/health' }, response: [{ code: 200 }] },
        {
          name: 'Partner feed',
          request: {
            method: 'GET', url: '{{baseUrl}}/partner/feed',
            auth: { type: 'apikey', apikey: [{ key: 'key', value: 'X-Partner-Key' }, { key: 'value', value: 'pk_live_9' }, { key: 'in', value: 'header' }] },
          },
        },
      ],
    },
  ],
}

function read(c: unknown): SpecSummary {
  const r = readSpec(JSON.stringify(c))
  if (!r.ok) throw new Error(r.error)
  return r.spec
}
const op = (s: SpecSummary, name: string): Operation => {
  const o = s.operations.find((x) => x.name === name)
  if (!o) throw new Error(`no ${name} in ${s.operations.map((x) => x.name).join(', ')}`)
  return o
}

describe('Postman collections', () => {
  const s = read(collection)

  it('reads the name and format, and asks for the base address when {{baseUrl}} has no value', () => {
    expect(s).toMatchObject({ title: 'Shop API', format: 'Postman collection v2.1', servers: ['{{baseUrl}}'] })
    expect(s.operations.map((o) => o.name)).toEqual(['Health', 'List orders', 'Get order', 'Partner feed', 'Create order', 'Upload receipt'])
    expect(s.notes.join(' ')).toMatch(/1 request with methods HawkEye can’t send was skipped/)
  })

  it('fills in known variables, keeps literal query values, and asks for the ones with no value', () => {
    const o = op(s, 'List orders')
    expect(o).toMatchObject({ path: '/v1/orders?limit=10', group: 'Orders', expectedStatus: 200, assertions: [] })
    expect(o.params).toEqual([{ name: 'cursor', in: 'query', value: '' }, { name: 'Accept', in: 'header', value: 'application/json' }])
  })

  it('turns :name path variables into values, filled in from the collection', () => {
    const o = op(s, 'Get order')
    expect(o).toMatchObject({ path: '/v1/orders/{id}', params: [{ name: 'id', in: 'path', value: 'ord_1' }] })
  })

  it('inherits auth from the collection and folders, and fills in values the collection holds', () => {
    expect(op(s, 'List orders').credential).toBe('Bearer token')
    expect(op(s, 'Health').credential).toBeNull()
    expect(op(s, 'Partner feed').credential).toBe('X-Partner-Key')
    expect(s.credentials).toEqual([
      { key: 'Bearer token', kind: 'bearer', label: 'Bearer token', prefill: undefined },
      { key: 'X-Partner-Key', kind: 'header', headerName: 'X-Partner-Key', label: 'API key in the X-Partner-Key header', prefill: { secret: 'pk_live_9' } },
    ])
  })

  it('keeps a write’s body, flags a variable left in it, and blocks form uploads', () => {
    expect(op(s, 'Create order')).toMatchObject({ write: true, body: '{"sku":"A-1","coupon":"{{coupon}}"}', expectedStatus: 201 })
    expect(op(s, 'Create order').hint).toMatch(/variable with no value/)
    expect(op(s, 'Upload receipt').blocked).toMatch(/form upload/)
    expect(ready(op(s, 'Upload receipt'), {}, '', s.credentials)).toBe(false)
  })

  it('builds the monitor from the base address the person types', () => {
    const o = op(s, 'List orders')
    const m = monitorFor(o, 'https://shop.example.com', { [paramKey(o, o.params[0])]: 'abc' }, '', s.credentials, { 'Bearer token': { secret: 't' } }, [])
    expect(m).toMatchObject({ url: 'https://shop.example.com/v1/orders?limit=10&cursor=abc', headers: [{ name: 'Accept', value: 'application/json' }],
      auth: { type: 'bearer', secret: 't' } })
  })

  it('keeps full addresses when requests go to several hosts, and blocks one whose host has no value', () => {
    const multi = read({
      info: { _postman_id: 'm', name: 'Mixed', schema: 'https://schema.getpostman.com/json/collection/v2.0.0/collection.json' },
      item: [
        { name: 'A', request: 'https://a.example.com/health' },
        { name: 'B', request: { method: 'GET', url: 'b.example.com/status', header: [{ key: 'Authorization', value: 'Bearer tok_b' }] } },
        { name: 'C', request: { method: 'GET', url: '{{other}}/x' } },
      ],
    })
    expect(multi).toMatchObject({ format: 'Postman collection v2.0', servers: [] })
    expect(op(multi, 'A')).toMatchObject({ path: 'https://a.example.com/health', absolute: true, health: true })
    expect(op(multi, 'B')).toMatchObject({ path: 'https://b.example.com/status', credential: 'Bearer token' })
    expect(multi.credentials[0].prefill).toEqual({ secret: 'tok_b' })
    expect(op(multi, 'C').blocked).toMatch(/starts with \{\{other\}\}/)
    expect(monitorFor(op(multi, 'A'), 'ignored', {}, '', [], {}, []).url).toBe('https://a.example.com/health')
  })

  it('explains Postman files that aren’t a v2 collection', () => {
    const err = (v: unknown) => {
      const r = readSpec(JSON.stringify(v))
      return r.ok ? 'parsed' : r.error
    }
    expect(err({ id: 'e', name: 'Staging', values: [{ key: 'baseUrl', value: 'https://x' }], _postman_variable_scope: 'environment' }))
      .toMatch(/Postman environment/)
    expect(err({ id: 'c', name: 'Old', order: [], requests: [] })).toMatch(/old v1 format/)
    expect(err({ info: { _postman_id: 'x', name: 'Empty', schema: 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json' }, item: [] }))
      .toMatch(/no requests/)
  })
})
