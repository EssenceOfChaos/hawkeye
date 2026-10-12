import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MonitorFormPage } from './MonitorFormPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function setup(postResponse: () => Promise<Response>) {
  document.cookie = 'csrf_token=t; path=/'
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: 300 } } })
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

  it('returns to the list after success', async () => {
    setup(() => json(201, { monitor: {} }))
    await fill('Home')
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
  })
})
