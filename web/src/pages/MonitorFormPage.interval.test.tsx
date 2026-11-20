import { render, screen, waitFor } from '@testing-library/react'
import userEvent, { type UserEvent } from '@testing-library/user-event'
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

// The form is large, which made the first test below time out now and then when the whole suite ran in parallel. Two costs dominated: role queries
// (getByRole/findByRole), which compute the accessibility tree of the whole form, about 100-200 ms each here and several times that under load (a
// findBy gives up after 1 s); and typing values one key at a time, which re-renders the form for every character. So these tests find elements by
// label or text, wait until the plan's minimum interval has loaded, and paste each value in one event.
async function fill(user: UserEvent, label: string, value: string) {
  await user.click(screen.getByLabelText(label))
  await user.paste(value)
}

describe('MonitorFormPage check interval', () => {
  it('offers 30-second checks when the plan allows them, and sends 30', async () => {
    const user = userEvent.setup()
    const fetchMock = setup(30)
    const thirty = await screen.findByLabelText('Check every 30 seconds')
    await fill(user, 'Name', 'Shop')
    await fill(user, 'URL', 'https://example.com')
    await user.click(thirty)
    expect(screen.getByLabelText('Check every (minutes)')).toBeDisabled()
    expect(screen.getByText(/kept for 30 days/)).toBeInTheDocument()
    await user.click(screen.getByText('Add monitor', { selector: 'button' }))
    await screen.findByText('Monitor list', { selector: 'h1' })
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(JSON.parse(post[1]!.body as string).check_interval_seconds).toBe(30)
  })

  it('leaves the interval to the default (a minute) when nothing is chosen', async () => {
    const user = userEvent.setup()
    const fetchMock = setup(30)
    await screen.findByLabelText('Check every 30 seconds')
    await fill(user, 'Name', 'Shop')
    await fill(user, 'URL', 'https://example.com')
    await user.click(screen.getByText('Add monitor', { selector: 'button' }))
    await screen.findByText('Monitor list', { selector: 'h1' })
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(JSON.parse(post[1]!.body as string)).not.toHaveProperty('check_interval_seconds')
  })

  it('has no 30-second switch on plans that check every minute or slower', async () => {
    setup(60)
    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled())
    await screen.findByText(/every 1 minute or slower/)
    expect(screen.queryByLabelText('Check every 30 seconds')).not.toBeInTheDocument()
  })

  it('has no 30-second switch for a request that changes data', async () => {
    const user = userEvent.setup()
    setup(30)
    await screen.findByLabelText('Check every 30 seconds')
    await user.selectOptions(screen.getByLabelText('Request method'), 'POST')
    expect(screen.queryByLabelText('Check every 30 seconds')).not.toBeInTheDocument()
  })
})
