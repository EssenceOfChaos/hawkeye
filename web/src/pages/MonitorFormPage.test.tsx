import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MonitorFormPage } from './MonitorFormPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function setup(postResponse: () => Promise<Response>, regionsResponse: () => Promise<Response> = () => json(200, {})) {
  document.cookie = 'csrf_token=t; path=/'
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: 300 } } })
      if (url.endsWith('/regions')) return regionsResponse()
      if (init?.method === 'POST') return postResponse()
      return json(200, {})
    }),
  )
  render(
    <MemoryRouter initialEntries={['/monitors/new']}>
      <Routes>
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors" element={<h1>Monitor list</h1>} />
      </Routes>
    </MemoryRouter>,
  )
}

async function fill(name: string) {
  await userEvent.type(screen.getByLabelText('Name'), name)
  await userEvent.clear(screen.getByLabelText('URL'))
  await userEvent.type(screen.getByLabelText('URL'), 'https://example.com')
}

describe('MonitorFormPage', () => {
  it('shows server-side field errors next to the field', async () => {
    setup(() => json(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { url: 'must be a publicly reachable address' } } } }))
    await fill('Home')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    expect(await screen.findByText('must be a publicly reachable address')).toBeInTheDocument()
    expect(screen.getByLabelText('URL')).toHaveAttribute('aria-invalid', 'true')
  })

  it('explains a plan limit with the next plan', async () => {
    setup(() => json(403, { error: { code: 'PLAN_LIMIT_REACHED', message: 'Your Free plan includes up to 5 monitors and all of them are in use.', details: { next_plan: { name: 'Pro', max_monitors: 50 } } } }))
    await fill('Sixth')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/Pro includes 50 monitors/)
  })

  it('sends headers, body, auth and assertions for an API check', async () => {
    setup(() => json(201, { monitor: {} }))
    const fetchMock = vi.mocked(globalThis.fetch)
    await fill('API')
    await userEvent.selectOptions(screen.getByLabelText('Request method'), 'POST')
    await userEvent.click(screen.getByRole('button', { name: 'Add header' }))
    await userEvent.type(screen.getByLabelText('Header 1 name'), 'X-Env')
    await userEvent.type(screen.getByLabelText('Header 1 value'), 'prod')
    await userEvent.type(screen.getByLabelText('Request body (optional)'), 'ping')
    await userEvent.selectOptions(screen.getByLabelText('Authentication'), 'bearer')
    await userEvent.type(screen.getByLabelText('Token'), 'tok')
    await userEvent.click(screen.getByRole('button', { name: 'Add assertion' }))
    await userEvent.selectOptions(screen.getByLabelText('Assertion 1 type'), 'json_path')
    await userEvent.type(screen.getByLabelText('Assertion 1 JSON path'), 'data.status')
    await userEvent.type(screen.getByLabelText('Assertion 1 value'), 'ok')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')!
    const sent = JSON.parse(post[1]!.body as string)
    expect(sent).toMatchObject({
      method: 'POST',
      headers: [{ name: 'X-Env', value: 'prod' }],
      body: 'ping',
      auth: { type: 'bearer', secret: 'tok' },
      assertions: [{ type: 'json_path', path: 'data.status', operator: 'equals', value: 'ok' }],
    })
  })

  it('does not send a body for GET and hides assertions for HEAD', async () => {
    setup(() => json(201, { monitor: {} }))
    await fill('Site')
    expect(screen.queryByLabelText('Request body (optional)')).not.toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Request method'), 'HEAD')
    expect(screen.queryByRole('button', { name: 'Add assertion' })).not.toBeInTheDocument()
    expect(screen.getByText(/HEAD responses have no body/)).toBeInTheDocument()
  })

  it('shows errors for a header row', async () => {
    setup(() => json(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { 'headers[0].name': 'use authentication instead, so the credentials stay hidden' } } } }))
    await fill('Home')
    await userEvent.click(screen.getByRole('button', { name: 'Add header' }))
    await userEvent.type(screen.getByLabelText('Header 1 name'), 'Authorization')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    expect(await screen.findByText(/use authentication instead/)).toBeInTheDocument()
    expect(screen.getByLabelText('Header 1 name')).toHaveAttribute('aria-invalid', 'true')
  })

  it('returns to the list after success', async () => {
    setup(() => json(201, { monitor: {} }))
    await fill('Home')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
  })
})

describe('MonitorFormPage editing', () => {
  it('keeps the stored secret unless a new one is entered', async () => {
    document.cookie = 'csrf_token=t; path=/'
    const monitor = {
      id: 'm1', name: 'API', url: 'https://api.example.com', method: 'GET', expected_status_code: null,
      check_interval_seconds: 300, effective_check_interval_seconds: 300, timeout_seconds: 10, enabled: true,
      current_status: 'up', last_checked_at: null, headers: [], body: null,
      auth: { type: 'basic', username: 'svc', secret_set: true }, assertions: [{ type: 'body_contains', value: 'ok' }],
    }
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: 300 } } })
      if (init?.method === 'PATCH') return json(200, { monitor })
      return json(200, { monitor })
    })
    vi.stubGlobal('fetch', fetchMock)
    render(
      <MemoryRouter initialEntries={['/monitors/m1/edit']}>
        <Routes>
          <Route path="/monitors/:monitorID/edit" element={<MonitorFormPage />} />
          <Route path="/monitors" element={<h1>Monitor list</h1>} />
        </Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByLabelText('Username')).toHaveValue('svc')
    expect(screen.getByText(/Leave empty to keep it/)).toBeInTheDocument()
    expect(screen.getByLabelText('Assertion 1 value')).toHaveValue('ok')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
    const patch = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    const sent = JSON.parse(patch[1]!.body as string)
    expect(sent.auth).toEqual({ type: 'basic', username: 'svc' })
    expect(sent.body).toBeNull()
  })

  it('warns that state-changing methods repeat on every check, and raises the minimum interval', async () => {
    setup(() => json(201, { monitor: {} }))
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Request method'), 'DELETE')
    expect(screen.getByRole('note')).toHaveTextContent(/repeated on every check/)
    expect(screen.getByRole('note')).toHaveTextContent(/verified email/)
    expect(screen.getByLabelText('Check every (minutes)')).toHaveAttribute('min', '5')
    await userEvent.selectOptions(screen.getByLabelText('Request method'), 'OPTIONS')
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
  })

  it('shows the server message when a write method needs a verified email', async () => {
    setup(() => json(403, { error: { code: 'EMAIL_NOT_VERIFIED', message: 'Verify your email address to monitor with POST, PUT, PATCH or DELETE requests.' } }))
    await fill('Writer')
    await userEvent.selectOptions(screen.getByLabelText('Request method'), 'POST')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/Verify your email address/)
  })

  it('offers locations only when the installation has several, limited by the plan', async () => {
    let sent: { regions?: string[] } = {}
    setup(
      () => json(201, { monitor: {} }),
      () => json(200, { regions: [{ code: 'us', name: 'US East' }, { code: 'eu', name: 'Europe' }, { code: 'ap', name: 'Asia' }], primary: 'us', max_regions: 2 }),
    )
    const post = (fetch as unknown as { mock: { calls: [string, RequestInit][] } }).mock
    await fill('Home')
    expect(await screen.findByLabelText('US East')).toBeChecked() // the primary is the default
    await userEvent.click(screen.getByLabelText('Europe'))
    expect(screen.getByLabelText('Asia')).toBeDisabled() // the plan allows two
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(post.calls.some(([, init]) => init?.method === 'POST')).toBe(true))
    sent = JSON.parse(post.calls.find(([, init]) => init?.method === 'POST')![1].body as string)
    expect(sent.regions).toEqual(['us', 'eu'])
  })

  it('hides the locations picker on a single-location installation', async () => {
    setup(() => json(201, { monitor: {} }), () => json(200, { regions: [{ code: 'primary', name: 'Primary' }], primary: 'primary', max_regions: 1 }))
    await fill('Home')
    expect(screen.queryByText('Check from')).not.toBeInTheDocument()
  })
})
