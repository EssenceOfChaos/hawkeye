import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MonitorFormPage } from '../pages/MonitorFormPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function setup(path: string) {
  document.cookie = 'csrf_token=t; path=/'
  const posts: Record<string, unknown>[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: 60, max_workflow_steps: 3 } } })
      if (init?.method === 'POST') {
        posts.push(JSON.parse(init.body as string))
        return json(201, { monitor: { id: 'm1' } })
      }
      return json(200, {})
    }),
  )
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors" element={<h1>Monitor list</h1>} />
        <Route path="/monitors/:id" element={<h1>Monitor page</h1>} />
      </Routes>
    </MemoryRouter>,
  )
  return posts
}

async function paste(button: string, field: string, command: string) {
  await userEvent.click(await screen.findByRole('button', { name: button }))
  await userEvent.click(screen.getByLabelText(field))
  await userEvent.paste(command)
  await userEvent.click(screen.getByRole('button', { name: 'Fill in the form' }))
}

const chrome = `curl 'https://api.example.com/v1/orders' \\
  -H 'authorization: Bearer sk_live_abc' \\
  -H 'x-env: prod' \\
  -H 'sec-fetch-mode: cors' \\
  --data-raw '{"sku":"A-1"}'`

describe('Paste a cURL command', () => {
  it('fills in an API check, moves the token into credentials, and puts back a left-out header on request', async () => {
    const posts = setup('/monitors/new')
    await paste('Paste a cURL command', 'cURL command', chrome)

    expect(screen.queryByLabelText('cURL command')).not.toBeInTheDocument() // the pasted text, token and all, is gone
    expect(screen.getByLabelText('Name')).toHaveValue('POST api.example.com/v1/orders')
    expect(screen.getByLabelText('URL')).toHaveValue('https://api.example.com/v1/orders')
    expect(screen.getByLabelText('Request method')).toHaveValue('POST')
    expect(screen.getByLabelText('Header 1 name')).toHaveValue('x-env')
    expect(screen.queryByLabelText('Header 2 name')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Authentication')).toHaveValue('bearer')
    expect(screen.getByLabelText('Token')).toHaveValue('sk_live_abc')
    expect(screen.getByRole('status')).toHaveTextContent('Filled in from your command')

    await userEvent.click(screen.getByText('What changed from the command'))
    await userEvent.click(screen.getByRole('button', { name: 'Put back the sec-fetch-mode header' }))
    expect(screen.getByLabelText('Header 2 name')).toHaveValue('sec-fetch-mode')

    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
    expect(posts[0]).toMatchObject({
      name: 'POST api.example.com/v1/orders', url: 'https://api.example.com/v1/orders', method: 'POST', body: '{"sku":"A-1"}',
      headers: [{ name: 'x-env', value: 'prod' }, { name: 'sec-fetch-mode', value: 'cors' }],
      auth: { type: 'bearer', secret: 'sk_live_abc' },
    })
  })

  it('keeps a name the person already typed', async () => {
    setup('/monitors/new')
    await userEvent.type(await screen.findByLabelText('Name'), 'Orders API')
    await paste('Paste a cURL command', 'cURL command', 'curl https://api.example.com/v1/orders')
    expect(screen.getByLabelText('Name')).toHaveValue('Orders API')
  })

  it('explains a command it can’t read and changes nothing', async () => {
    setup('/monitors/new')
    await paste('Paste a cURL command', 'cURL command', 'wget https://api.example.com')
    expect(screen.getByText(/It should start with curl/)).toBeInTheDocument()
    expect(screen.getByLabelText('cURL command')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByLabelText('URL')).toHaveValue('https://')
  })

  it('warns about an address HawkEye can’t reach, but still fills in the form', async () => {
    setup('/monitors/new')
    await paste('Paste a cURL command', 'cURL command', 'curl http://localhost:3000/health')
    expect(screen.getByRole('status')).toHaveTextContent(/check this first.*port 3000.*use the public address/s)
    expect(screen.getByLabelText('URL')).toHaveValue('http://localhost:3000/health')
  })

  it('fills in a workflow step and keeps its credential as a hidden secret', async () => {
    const posts = setup('/monitors/new?type=workflow')
    await userEvent.type((await screen.findAllByLabelText('Name', { exact: true }))[0], 'Orders')
    await paste('Paste a cURL command for step 1', 'cURL command for step 1',
      `curl -m 5 -H 'X-API-Key: k-123' https://api.example.com/v1/orders`)

    expect(screen.getAllByLabelText('URL')[0]).toHaveValue('https://api.example.com/v1/orders')
    expect(screen.getByLabelText('Secret 1 name')).toHaveValue('x_api_key')
    expect(screen.getByLabelText('Secret x_api_key value')).toHaveValue('k-123')
    expect(screen.getByLabelText('Header value')).toHaveValue('{{secret.x_api_key}}')

    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor page' })).toBeInTheDocument())
    expect(posts[0]).toMatchObject({
      type: 'workflow',
      secrets: { x_api_key: 'k-123' },
      steps: [{ method: 'GET', url: 'https://api.example.com/v1/orders', headers: [], timeout_seconds: 5,
        auth: { type: 'header', header_name: 'X-API-Key', secret: '{{secret.x_api_key}}' } }],
    })
  })
})
