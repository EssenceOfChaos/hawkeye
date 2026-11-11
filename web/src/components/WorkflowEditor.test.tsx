import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MonitorFormPage } from '../pages/MonitorFormPage'
import { WorkflowRun } from './WorkflowRun'
import { draftsFrom, secretsInput, stepsInput } from '../workflowDrafts'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function setup(onPost: (url: string, body: Record<string, unknown>) => Promise<Response>) {
  document.cookie = 'csrf_token=t; path=/'
  const posts: { url: string; body: Record<string, unknown> }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: 60, max_workflow_steps: 3 } } })
      if (init?.method === 'POST') {
        const body = JSON.parse(init.body as string)
        posts.push({ url, body })
        return onPost(url, body)
      }
      return json(200, {})
    }),
  )
  render(
    <MemoryRouter initialEntries={['/monitors/new?type=workflow']}>
      <Routes>
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors/:id" element={<h1>Monitor page</h1>} />
      </Routes>
    </MemoryRouter>,
  )
  return posts
}

async function buildTwoSteps() {
  const names = () => screen.getAllByLabelText('Name', { exact: true })
  await userEvent.type(names()[0], 'Checkout')
  await userEvent.clear(screen.getAllByLabelText('URL')[0])
  await userEvent.type(screen.getAllByLabelText('URL')[0], 'https://api.example.com/login')
  await userEvent.click(screen.getByRole('button', { name: 'Save a value' }))
  await userEvent.type(screen.getByLabelText('Step 1, value 1: JSON path'), 'data.token')
  await userEvent.type(screen.getByLabelText('Step 1, value 1: name'), 'token')
  await userEvent.click(screen.getByRole('button', { name: 'Add secret' }))
  await userEvent.type(screen.getByLabelText('Secret 1 name'), 'password')
  await userEvent.type(screen.getByLabelText('Secret password value'), 'hunter22')
  await userEvent.click(screen.getByRole('button', { name: 'Add step' }))
  await userEvent.clear(names()[2])
  await userEvent.type(names()[2], 'Orders')
  await userEvent.clear(screen.getAllByLabelText('URL')[1])
  await userEvent.type(screen.getAllByLabelText('URL')[1], 'https://api.example.com/orders')
}

// These type a whole workflow, which takes several seconds when the full suite runs in parallel.
describe('the multi-step API check editor', { timeout: 20_000 }, () => {
  it('offers later steps the values earlier steps save, and the secrets', async () => {
    setup(() => json(500, {}))
    await buildTwoSteps()
    const step2 = screen.getAllByRole('group').find((g) => g.querySelector('legend')?.textContent?.includes('Step 2'))!
    expect(within(step2).getByRole('button', { name: 'Copy {{token}}' })).toBeInTheDocument()
    expect(within(step2).getByRole('button', { name: 'Copy {{secret.password}}' })).toBeInTheDocument()
    const step1 = screen.getAllByRole('group').find((g) => g.querySelector('legend')?.textContent?.includes('Step 1'))!
    expect(within(step1).queryByRole('button', { name: 'Copy {{token}}' })).not.toBeInTheDocument() // a step can't use what it saves itself
  })

  it('runs a test without saving and shows each step', async () => {
    const posts = setup((url) => url.endsWith('/test-workflow')
      ? json(200, { result: { success: false, status_code: 401, response_time_ms: 90, error_category: 'http_status', error_summary: 'Step 2 (Orders): Received HTTP 401',
        steps: [{ name: 'Sign in', success: true, status_code: 200, response_time_ms: 60 }, { name: 'Orders', success: false, status_code: 401, response_time_ms: 30, error_summary: 'Received HTTP 401' }] } })
      : json(500, {}))
    await buildTwoSteps()
    await userEvent.click(screen.getByRole('button', { name: 'Add step' }))
    await userEvent.click(screen.getByRole('button', { name: 'Run test' }))
    const run = await screen.findByRole('list', { name: 'Test run' })
    const items = within(run).getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('Sign in: passed')
    expect(items[1]).toHaveTextContent('Orders: failed')
    expect(items[1]).toHaveTextContent('Received HTTP 401')
    expect(items[2]).toHaveTextContent('Not run')
    expect(screen.getByText('The check would fail: Step 2 (Orders): Received HTTP 401')).toBeInTheDocument()
    expect(posts).toHaveLength(1)
    expect(posts[0].body.secrets).toEqual({ password: 'hunter22' })
  })

  it('saves the steps, secrets and cookies setting', async () => {
    const posts = setup(() => json(201, { monitor: { id: 'm1' } }))
    await buildTwoSteps()
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await screen.findByRole('heading', { name: 'Monitor page' })
    const body = posts[0].body as { type: string; steps: { name: string; extractions: unknown[] }[]; secrets: unknown; cookies: boolean }
    expect(body.type).toBe('workflow')
    expect(body.steps.map((s) => s.name)).toEqual(['Sign in', 'Orders'])
    expect(body.steps[0].extractions).toEqual([{ name: 'token', from: 'json_path', path: 'data.token' }])
    expect(body.secrets).toEqual({ password: 'hunter22' })
    expect(body.cookies).toBe(false)
  })

  it('stops adding steps at the plan limit', async () => {
    setup(() => json(500, {}))
    await userEvent.click(screen.getByRole('button', { name: 'Add step' }))
    await userEvent.click(screen.getByRole('button', { name: 'Add step' }))
    expect(screen.getByRole('button', { name: 'Add step' })).toBeDisabled()
    expect(screen.getByText('Your plan allows up to 3 steps.')).toBeInTheDocument()
  })
})

describe('workflow drafts', () => {
  it('keep stored secrets unless changed or removed', () => {
    const d = draftsFrom({
      cookies: true, secrets: [{ name: 'password', set: true }, { name: 'old', set: true }],
      steps: [{ name: 'A', method: 'GET', url: 'https://a.example.com/', headers: [], body: null, auth: { type: 'bearer', secret_set: true },
        expected_status_code: null, assertions: [], extractions: [], timeout_seconds: 10 }],
    })
    d.secrets[1].removed = true
    expect(secretsInput(d.secrets)).toEqual({ old: null }) // password untouched: not sent, so it is kept
    expect(stepsInput(d.steps)[0].auth).toEqual({ type: 'bearer' }) // no secret sent: the stored one is kept
  })
})

describe('secrets an assistant named', () => {
  it('show as not entered, are sent once typed, and can be removed', () => {
    const d = draftsFrom({ cookies: false, secrets: [{ name: 'password', set: false }, { name: 'key', set: false }], steps: [] })
    expect(d.secrets.every((s) => s.pending && !s.stored)).toBe(true)
    d.secrets[0].value = 'typed-by-a-person'
    d.secrets[1].removed = true
    expect(secretsInput(d.secrets)).toEqual({ password: 'typed-by-a-person', key: null })
  })
})

describe('WorkflowRun', () => {
  it('marks passed, failed and skipped steps', () => {
    render(<WorkflowRun label="Run" names={['A', 'B', 'C']} outcomes={[{ name: 'A', success: true, status_code: 200, response_time_ms: 5 },
      { name: 'B', success: false, status_code: null, response_time_ms: null, error_summary: 'DNS lookup failed' }]} />)
    const items = screen.getAllByRole('listitem')
    expect(items[0]).toHaveClass('d-step-success')
    expect(items[1]).toHaveClass('d-step-error')
    expect(items[1]).toHaveTextContent('DNS lookup failed')
    expect(items[2]).not.toHaveClass('d-step-success')
    expect(items[2]).toHaveTextContent('Not run')
  })
})
