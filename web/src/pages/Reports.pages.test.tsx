import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DashboardPage } from './DashboardPage'
import { MonitorDetailPage } from './MonitorDetailPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const plan = (days: number) => ({ code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: days })
const usage = (days: number) => ({ usage: { plan: plan(days), monitors_used: 1, monitor_limit: 5, over_limit: false, next_plan: null } })
const range = { from: '2026-10-11T00:00:00Z', to: '2026-10-12T00:00:00Z', preset: '24h', clamped_to_retention: false, retention_days: 7 }
const uptime = { from: range.from, to: range.to, clamped_to_retention: false, total_checks: 288, successful_checks: 287, failed_checks: 1, uptime_percent: 99.652, insufficient_data: false, min_checks: 5, avg_response_ms: 180, method: 'check_based' }

function mount(ui: React.ReactNode, path: string, route: string, handler: (url: string) => Promise<Response>) {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn((url: string) => { calls.push(url); return handler(url) }))
  render(<MemoryRouter initialEntries={[path]}><Routes><Route path={route} element={ui} /></Routes></MemoryRouter>)
  return calls
}

describe('DashboardPage', () => {
  const handler = (monitors: object, openIncidents = 0) => (url: string) => {
    if (url.includes('/usage')) return json(200, usage(7))
    if (url.includes('/dashboard/summary')) return json(200, { range, summary: { monitors, open_incidents: openIncidents, uptime, generated_at: range.to } })
    if (url.includes('/dashboard/response-times')) return json(200, { range, bucket_seconds: 900, points: [{ t: '2026-10-11T10:00:00Z', avg_ms: 180, max_ms: 300, checks: 3, failed: 0 }] })
    if (url.includes('/recent-incidents')) return json(200, { incidents: [{ id: 'i1', monitor_id: 'm1', monitor_name: 'Client Site', started_at: '2026-10-12T10:00:00Z', resolved_at: null, status: 'open', cause_category: 'timeout', summary: 'Request timed out', duration_seconds: 600 }] })
    return json(404, {})
  }

  it('leads with one uptime figure, counts in words, ongoing incidents and the recent list', async () => {
    mount(<DashboardPage />, '/dashboard', '/dashboard', handler({ total: 4, up: 2, down: 1, pending: 0, paused: 1 }, 1))
    expect(await screen.findByText('99.652%')).toBeInTheDocument()
    expect(screen.getByText(/1 ongoing incident\./)).toBeInTheDocument()
    expect(screen.getByText('Up')).toBeInTheDocument()
    expect(screen.getByText('Down')).toBeInTheDocument()
    expect(await screen.findByText('Client Site')).toBeInTheDocument()
    expect(screen.getAllByText(/Check-based uptime/)).toHaveLength(1) // exactly one hero figure
  })

  it('only offers ranges the plan keeps, and re-queries everything when the range changes', async () => {
    const calls = mount(<DashboardPage />, '/dashboard', '/dashboard', handler({ total: 1, up: 1, down: 0, pending: 0, paused: 0 }))
    await screen.findByText('99.652%')
    await waitFor(() => expect(screen.queryByRole('radio', { name: 'Last 30 days' })).not.toBeInTheDocument())
    await userEvent.click(screen.getByRole('radio', { name: 'Last 7 days' }))
    await waitFor(() => {
      expect(calls.some((c) => c.includes('/dashboard/summary?range=7d'))).toBe(true)
      expect(calls.some((c) => c.includes('/dashboard/response-times?range=7d'))).toBe(true)
    })
  })

  it('prompts a new user to add a first monitor', async () => {
    mount(<DashboardPage />, '/dashboard', '/dashboard', handler({ total: 0, up: 0, down: 0, pending: 0, paused: 0 }))
    expect(await screen.findByRole('heading', { name: 'Add your first website' })).toBeInTheDocument()
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })
})

describe('MonitorDetailPage', () => {
  const monitor = { id: 'm1', name: 'Client Site', url: 'https://client.example.com', method: 'GET', expected_status_code: null, check_interval_seconds: 300, effective_check_interval_seconds: 300, timeout_seconds: 10, enabled: true, current_status: 'up', last_checked_at: '2026-10-12T11:55:00Z' }
  const handler = (url: string) => {
    if (url.includes('/usage')) return json(200, usage(7))
    if (url.endsWith('/monitors/m1')) return json(200, { monitor })
    if (url.includes('/uptime')) return json(200, { range, uptime })
    if (url.includes('/response-times')) return json(200, { range, bucket_seconds: 900, points: [] })
    if (url.includes('/results')) return json(200, { range, total: 2, results: [
      { id: 2, checked_at: '2026-10-12T11:55:00Z', success: false, http_status_code: 503, response_time_ms: null, error_category: 'http_status', error_summary: 'Received HTTP 503' },
      { id: 1, checked_at: '2026-10-12T11:50:00Z', success: true, http_status_code: 200, response_time_ms: 180, error_category: null, error_summary: null },
    ] })
    if (url.includes('/incidents')) return json(200, { incidents: [], total: 0 })
    return json(404, {})
  }
  const ui = <Routes><Route path="/monitors/:monitorID" element={<MonitorDetailPage />} /></Routes>

  it('shows uptime, history with text results, and the monitor header', async () => {
    mount(<MonitorDetailPage />, '/monitors/m1', '/monitors/:monitorID', handler)
    expect(await screen.findByRole('heading', { name: 'Client Site' })).toBeInTheDocument()
    expect(await screen.findByText('99.652%')).toBeInTheDocument()
    expect(await screen.findByText('Received HTTP 503')).toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText('Success')).toBeInTheDocument()
    expect(screen.getByText('No incidents for this website.')).toBeInTheDocument()
    expect(ui).toBeTruthy()
  })

  it('handles a missing monitor', async () => {
    mount(<MonitorDetailPage />, '/monitors/zzz', '/monitors/:monitorID', (url) =>
      url.includes('/usage') ? json(200, usage(7)) : json(404, { error: { code: 'NOT_FOUND', message: 'Monitor not found.' } }))
    expect(await screen.findByRole('heading', { name: 'Monitor not found' })).toBeInTheDocument()
  })
})
