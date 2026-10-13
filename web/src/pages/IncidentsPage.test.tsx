import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatDuration, IncidentsPage } from './IncidentsPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const incident = (o: object) => ({
  id: 'i1', monitor_id: 'm1', monitor_name: 'Client Site', started_at: '2026-10-12T14:30:00Z', resolved_at: null,
  status: 'open', cause_category: 'timeout', summary: 'Request timed out', duration_seconds: 3700, ...o,
})

function renderPage(list: unknown[]) {
  vi.stubGlobal('fetch', vi.fn(() => json(200, { incidents: list, total: list.length })))
  render(<MemoryRouter><IncidentsPage /></MemoryRouter>)
}

describe('IncidentsPage', () => {
  it('lists ongoing and resolved incidents with text status and duration', async () => {
    renderPage([incident({}), incident({ id: 'i2', status: 'resolved', resolved_at: '2026-10-12T15:00:00Z', duration_seconds: 1800, summary: null, cause_category: 'dns' })])
    const table = await screen.findByRole('table')
    expect(within(table).getByText('Ongoing')).toBeInTheDocument()
    expect(within(table).getByText('Resolved')).toBeInTheDocument()
    expect(screen.getByText('1h 1m so far')).toBeInTheDocument()
    expect(screen.getByText('30m')).toBeInTheDocument()
    expect(screen.getByText('DNS lookup failed')).toBeInTheDocument()
  })

  it('has a friendly empty state', async () => {
    renderPage([])
    expect(await screen.findByText(/No incidents to report/)).toBeInTheDocument()
  })
})

describe('formatDuration', () => {
  it('formats compactly', () => {
    expect(formatDuration(5)).toBe('5s')
    expect(formatDuration(300)).toBe('5m')
    expect(formatDuration(7500)).toBe('2h 5m')
    expect(formatDuration(90000)).toBe('1d 1h')
  })
})
