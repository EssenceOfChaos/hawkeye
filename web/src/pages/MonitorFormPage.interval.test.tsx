import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MonitorFormPage } from './MonitorFormPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function setup(minimum: number) {
  document.cookie = 'csrf_token=t; path=/'
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    if (url.endsWith('/usage')) return json(200, { usage: { plan: { minimum_check_interval_seconds: minimum } } })
    if (init?.method === 'POST') return json(201, { monitor: {} })
    return json(200, {})
  })
  vi.stubGlobal('fetch', fetchMock)
  render(
    <MemoryRouter initialEntries={['/monitors/new']}>
      <Routes>
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors" element={<h1>Monitor list</h1>} />
      </Routes>
    </MemoryRouter>,
  )
  return fetchMock
}

describe('MonitorFormPage check interval', () => {
  it('offers 30-second checks when the plan allows them, and sends 30', async () => {
    const fetchMock = setup(30)
    await userEvent.type(screen.getByLabelText('Name'), 'Shop')
    await userEvent.type(screen.getByLabelText('URL'), 'https://example.com')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'Check every 30 seconds' }))
    expect(screen.getByLabelText('Check every (minutes)')).toBeDisabled()
    expect(screen.getByText(/kept for 30 days/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(JSON.parse(post[1]!.body as string).check_interval_seconds).toBe(30)
  })

  it('leaves the interval to the default (a minute) when nothing is chosen', async () => {
    const fetchMock = setup(30)
    await userEvent.type(screen.getByLabelText('Name'), 'Shop')
    await userEvent.type(screen.getByLabelText('URL'), 'https://example.com')
    await screen.findByRole('checkbox', { name: 'Check every 30 seconds' })
    await userEvent.click(screen.getByRole('button', { name: 'Add monitor' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Monitor list' })).toBeInTheDocument())
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(JSON.parse(post[1]!.body as string)).not.toHaveProperty('check_interval_seconds')
  })

  it('has no 30-second switch on plans that check every minute or slower', async () => {
    setup(60)
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled())
    await screen.findByText(/every 1 minute or slower/)
    expect(screen.queryByRole('checkbox', { name: 'Check every 30 seconds' })).not.toBeInTheDocument()
  })

  it('has no 30-second switch for a request that changes data', async () => {
    setup(30)
    await screen.findByRole('checkbox', { name: 'Check every 30 seconds' })
    await userEvent.selectOptions(screen.getByLabelText('Request method'), 'POST')
    expect(screen.queryByRole('checkbox', { name: 'Check every 30 seconds' })).not.toBeInTheDocument()
  })
})
