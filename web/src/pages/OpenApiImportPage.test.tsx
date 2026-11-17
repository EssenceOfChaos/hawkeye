import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { OpenApiImportPage } from './OpenApiImportPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const spec = `openapi: 3.0.3
info: { title: Orders API, version: '1' }
servers: [{ url: 'https://api.example.com/v1' }]
security: [{ bearerAuth: [] }]
paths:
  /health:
    get:
      security: []
      responses: { '200': { description: ok } }
  /orders:
    get:
      summary: List orders
      responses:
        '200':
          description: ok
          content: { application/json: { schema: { type: object, required: [items], properties: { items: { type: array } } } } }
    post:
      summary: Create an order
      requestBody: { required: true, content: { application/json: { schema: { type: object } } } }
      responses: { '201': { description: created } }
  /orders/{id}:
    get:
      summary: Get an order
      parameters: [{ name: id, in: path, required: true, schema: { type: string } }]
      responses: { '200': { description: ok } }
  /me:
    get:
      summary: Who am I
      responses: { '200': { description: ok } }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
`

function setup(opts: { tags?: { id: string; name: string }[]; room?: number; specText?: string } = {}) {
  document.cookie = 'csrf_token=t; path=/'
  const posts: { url: string; body: Record<string, unknown> }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { monitors_used: 1, monitor_limit: 1 + (opts.room ?? 20), plan: {}, over_limit: false, next_plan: null } })
      if (url.endsWith('/tags') && init?.method !== 'POST') return json(200, { tags: opts.tags ?? [] })
      if (init?.method === 'POST') {
        const body = JSON.parse(init.body as string)
        posts.push({ url, body })
        if (url.endsWith('/tags')) return json(201, { tag: { id: 'tag-new', name: body.name } })
        if (String(body.url).endsWith('/me')) {
          return json(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { url: 'must be a publicly reachable address' } } } })
        }
        return json(201, { monitor: { id: `m${posts.length}` } })
      }
      return json(200, {})
    }),
  )
  render(<MemoryRouter><OpenApiImportPage /></MemoryRouter>)
  return posts
}

async function readIt(text = spec) {
  await userEvent.click(screen.getByLabelText('Or paste it'))
  await userEvent.paste(text)
  await userEvent.click(screen.getByRole('button', { name: 'Read the file' }))
  await screen.findByRole('heading', { name: 'Orders API' })
}

const box = (re: RegExp) => screen.getByRole('checkbox', { name: re })

describe('OpenApiImportPage', () => {
  it('ticks the useful checks, asks for the token, tags them and adds them, showing the server’s reason for one that fails', async () => {
    const posts = setup()
    await readIt()

    expect(screen.getByLabelText('API address')).toHaveValue('https://api.example.com/v1')
    expect(box(/GET \/health/)).toBeChecked()
    expect(box(/GET \/orders List orders/)).toBeChecked()
    expect(box(/GET \/me/)).toBeChecked()
    expect(box(/GET \/orders\/\{id\}/)).not.toBeChecked() // needs a real ID
    expect(box(/POST \/orders/)).not.toBeChecked() // changes data
    expect(screen.getByText('Needs a value for id.')).toBeInTheDocument()
    expect(screen.getByText(/Expects status 200, with items in the response/)).toBeInTheDocument()

    const add = screen.getByRole('button', { name: 'Add 3 checks' })
    expect(add).toBeDisabled()
    expect(screen.getByText('Enter the credentials the ticked checks use.')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Bearer token (bearerAuth)'), 'tok_1')
    expect(add).toBeEnabled()
    await userEvent.click(add)

    await screen.findByText(/Added 2 checks/)
    expect(posts.map((p) => p.url.replace(/.*\/api\/v1/, ''))).toEqual(['/tags', '/monitors', '/monitors', '/monitors'])
    expect(posts[0].body).toEqual({ name: 'Orders API' })
    expect(posts[1].body).toMatchObject({ name: 'GET /health', url: 'https://api.example.com/v1/health', auth: { type: 'none' }, tag_ids: ['tag-new'] })
    expect(posts[2].body).toMatchObject({
      name: 'List orders', url: 'https://api.example.com/v1/orders', expected_status_code: 200, auth: { type: 'bearer', secret: 'tok_1' },
      assertions: [{ type: 'json_path', path: 'items', operator: 'exists' }],
    })
    expect(screen.getByText('url must be a publicly reachable address')).toBeInTheDocument()
    expect(box(/GET \/health/)).toBeDisabled() // added
    expect(box(/GET \/me/)).toBeChecked() // still ticked, to fix and add again
    expect(screen.getByRole('button', { name: 'Add 1 check' })).toBeInTheDocument()
  })

  it('asks for the values and body a check needs before it can be added, and reuses an existing tag', async () => {
    const posts = setup({ tags: [{ id: 'tag-old', name: 'orders api' }] })
    await readIt()
    for (const re of [/GET \/health/, /GET \/orders List orders/, /GET \/me/]) await userEvent.click(box(re))

    await userEvent.click(box(/GET \/orders\/\{id\}/))
    await userEvent.click(box(/POST \/orders/))
    expect(screen.getByText(/Changes data/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add 2 checks' })).toBeDisabled()
    expect(screen.getByText(/2 to go/)).toBeInTheDocument()

    await userEvent.type(screen.getByLabelText(/id \(in the path\)/), 'ord_42')
    await userEvent.click(screen.getByLabelText('Request body'))
    await userEvent.paste('{"sku":"TEST"}')
    await userEvent.type(screen.getByLabelText('Bearer token (bearerAuth)'), 'tok')
    await userEvent.click(screen.getByRole('button', { name: 'Add 2 checks' }))

    await screen.findByText(/Added 2 checks/)
    expect(posts.map((p) => p.url.replace(/.*\/api\/v1/, ''))).toEqual(['/monitors', '/monitors'])
    expect(posts[0].body).toMatchObject({ url: 'https://api.example.com/v1/orders/ord_42', tag_ids: ['tag-old'] })
    expect(posts[1].body).toMatchObject({ method: 'POST', body: '{"sku":"TEST"}', tag_ids: ['tag-old'] })
  })

  it('needs a full public address when the spec only has a relative one', async () => {
    setup()
    await readIt(spec.replace("servers: [{ url: 'https://api.example.com/v1' }]", "servers: [{ url: '/v1' }]"))
    expect(screen.getByLabelText('API address')).toHaveValue('')
    expect(screen.getByText(/The file gives \/v1, which isn’t a full address/)).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Bearer token (bearerAuth)'), 'tok')
    expect(screen.getByRole('button', { name: /Add 3 checks/ })).toBeDisabled()
    await userEvent.type(screen.getByLabelText('API address'), 'http://localhost:8080')
    expect(screen.getByText(/only reachable on your own machine/)).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('API address'))
    await userEvent.type(screen.getByLabelText('API address'), 'https://api.example.com/v1')
    expect(screen.getByRole('button', { name: /Add 3 checks/ })).toBeEnabled()
  })

  it('never ticks more than the plan has room for', async () => {
    setup({ room: 2 })
    await readIt()
    expect(screen.getAllByRole('checkbox', { checked: true }).filter((c) => c.id.startsWith('op-'))).toHaveLength(2)
    expect(box(/GET \/me/)).toBeDisabled()
    expect(within(screen.getByRole('heading', { name: 'Orders API' }).closest('section')!).getByText(/room for 2 more monitors/)).toBeInTheDocument()
  })

  it('imports a Postman collection, asking for {{baseUrl}} and filling in the key the collection holds', async () => {
    const posts = setup()
    const collection = JSON.stringify({
      info: { _postman_id: 'c', name: 'Orders API', schema: 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json' },
      variable: [{ key: 'baseUrl', value: '' }],
      auth: { type: 'apikey', apikey: [{ key: 'key', value: 'X-API-Key' }, { key: 'value', value: 'k_123' }] },
      item: [{ name: 'Health', request: { method: 'GET', url: '{{baseUrl}}/health' }, response: [{ code: 200 }] }],
    })
    await readIt(collection)
    expect(screen.getByText(/Postman collection v2.1/)).toBeInTheDocument()
    expect(screen.getByText(/The file gives \{\{baseUrl\}\}, which isn’t a full address \(in Postman/)).toBeInTheDocument()
    expect(screen.getByLabelText('API key in the X-API-Key header (X-API-Key)')).toHaveValue('k_123')
    await userEvent.type(screen.getByLabelText('API address'), 'https://api.example.com')
    await userEvent.click(screen.getByRole('button', { name: 'Add 1 check' }))
    await screen.findByText(/Added 1 check/)
    expect(posts.at(-1)!.body).toMatchObject({ name: 'Health', url: 'https://api.example.com/health', expected_status_code: 200,
      auth: { type: 'header', header_name: 'X-API-Key', secret: 'k_123' } })
  })

  it('explains a file it can’t read', async () => {
    setup()
    await userEvent.click(screen.getByLabelText('Or paste it'))
    await userEvent.paste('{"hello": "world"}')
    await userEvent.click(screen.getByRole('button', { name: 'Read the file' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/isn’t an OpenAPI 3 or Swagger 2 spec/))
  })
})
