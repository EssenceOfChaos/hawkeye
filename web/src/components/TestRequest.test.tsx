import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MonitorFormPage } from '../pages/MonitorFormPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const ok = (body: string, checks: { passed: boolean; message: string | null }[] = []) => ({
  result: { success: checks.every((c) => c.passed), status_code: 200, response_time_ms: 84, error_category: null, error_summary: null,
    content_type: 'application/json', final_url: 'https://api.example.com/health', timings: null },
  response: { body, truncated: false, binary: false },
  checks,
})

function setup(onTest: (body: Record<string, unknown>) => Promise<Response>, path = '/monitors/new') {
  document.cookie = 'csrf_token=t; path=/'
  const posts: { url: string; body: Record<string, unknown> }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: 60 } } })
    if (url.endsWith('/monitors/m1')) {
      return json(200, { monitor: { id: 'm1', type: 'http', name: 'API', url: 'https://api.example.com/health', method: 'GET', effective_check_interval_seconds: 60,
        auth: { type: 'bearer', secret_set: true }, assertions: [] } })
    }
    if (init?.method === 'POST') {
      const body = JSON.parse(init.body as string)
      posts.push({ url, body })
      return url.endsWith('/test-request') ? onTest(body) : json(201, { monitor: {} })
    }
    return json(200, {})
  }))
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors/:monitorID/edit" element={<MonitorFormPage />} />
        <Route path="/monitors" element={<h1>Monitor list</h1>} />
      </Routes>
    </MemoryRouter>,
  )
  return posts
}

describe('Test request', () => {
  it('shows a JSON response and turns a clicked field into a check that is saved', async () => {
    const posts = setup(() => json(200, ok('{"database":{"status":"ok"},"queue_size":512}')))
    await userEvent.type(screen.getByLabelText('Name'), 'API')
    await userEvent.clear(screen.getByLabelText('URL'))
    await userEvent.type(screen.getByLabelText('URL'), 'https://api.example.com/health')
    await userEvent.click(screen.getByRole('button', { name: 'Send test request' }))

    const tree = await screen.findByRole('list', { name: 'Response fields' })
    expect(screen.getByText('The monitor would count as online.')).toBeInTheDocument()
    expect(posts[0].body).toMatchObject({ url: 'https://api.example.com/health', method: 'GET', assertions: [] })
    expect(posts[0].body.monitor_id).toBeUndefined()

    await userEvent.click(within(tree).getByRole('button', { name: /queue_size/ }))
    await userEvent.click(screen.getByRole('button', { name: 'is at most 512' }))
    expect(screen.getByRole('status')).toHaveTextContent('Added: queue_size is at most 512.')
    expect(screen.getByLabelText('JSON check 1 path')).toHaveValue('queue_size')
    expect(screen.getByLabelText('JSON check 1 operator')).toHaveValue('less_or_equal')
    expect(screen.getByLabelText('JSON check 1 value')).toHaveValue('512')

    await userEvent.click(within(tree).getByRole('button', { name: /status/ }))
    await userEvent.click(screen.getByRole('button', { name: 'equals "ok"' }))
    expect(screen.getByLabelText('JSON check 2 path')).toHaveValue('database.status')

    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
    expect(posts.at(-1)!.body.assertions).toEqual([
      { type: 'json_path', path: 'queue_size', operator: 'less_or_equal', value: '512' },
      { type: 'json_path', path: 'database.status', operator: 'equals', value: 'ok' },
    ])
  })

  it('lists each check with its result, and shows a text response as text', async () => {
    setup(() => json(200, { ...ok('<html>Welcome</html>', [{ passed: true, message: null }, { passed: false, message: 'Response body is not valid JSON' }]),
      result: { ...ok('').result, success: false, error_summary: 'Response body is not valid JSON', content_type: 'text/html' } }))
    await userEvent.click(screen.getByRole('button', { name: 'Add JSON check' }))
    await userEvent.type(screen.getByLabelText('JSON check 1 path'), 'status')
    await userEvent.selectOptions(screen.getByLabelText('JSON check 1 operator'), 'exists')
    await userEvent.click(screen.getByRole('button', { name: 'Add JSON check' }))
    await userEvent.type(screen.getByLabelText('JSON check 2 path'), 'n')
    await userEvent.selectOptions(screen.getByLabelText('JSON check 2 operator'), 'greater_than')
    expect(screen.getByLabelText('JSON check 2 value')).toHaveAttribute('placeholder', '500')
    await userEvent.type(screen.getByLabelText('JSON check 2 value'), '5')
    await userEvent.click(screen.getByRole('button', { name: 'Send test request' }))

    const checks = await screen.findByRole('list', { name: 'Checks' })
    expect(checks).toHaveTextContent('status exists')
    expect(checks).toHaveTextContent('n is greater than 5: Response body is not valid JSON')
    expect(screen.getByLabelText('Response body')).toHaveTextContent('<html>Welcome</html>')
    expect(screen.getByText('The monitor would count as offline.')).toBeInTheDocument()
  })

  it('shows field errors on the form', async () => {
    setup(() => json(400, { error: { code: 'VALIDATION_ERROR', message: 'bad', details: { fields: { url: 'must be a publicly reachable address' } } } }))
    await userEvent.click(screen.getByRole('button', { name: 'Send test request' }))
    expect(await screen.findByText('Fix the highlighted fields, then send the test again.')).toBeInTheDocument()
    expect(screen.getByText('must be a publicly reachable address')).toBeInTheDocument()
    expect(screen.getByLabelText('URL')).toHaveAttribute('aria-invalid', 'true')
  })

  it('uses the saved credential when editing, without sending it', async () => {
    const posts = setup(() => json(200, ok('{}')), '/monitors/m1/edit')
    await screen.findByDisplayValue('https://api.example.com/health')
    await userEvent.click(screen.getByRole('button', { name: 'Send test request' }))
    expect(await screen.findByLabelText('Response body')).toHaveTextContent('{}')
    expect(posts[0].body).toMatchObject({ monitor_id: 'm1', auth: { type: 'bearer' } })
    expect(posts[0].body.auth).not.toHaveProperty('secret')
  })
})
