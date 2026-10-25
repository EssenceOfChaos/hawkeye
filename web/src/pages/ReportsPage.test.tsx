import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { reportMonths } from '../format'
import { ReportsPage } from './ReportsPage'

afterEach(() => vi.unstubAllGlobals())
beforeEach(() => {
  document.cookie = 'csrf_token=t; path=/'
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const branding = (o: Record<string, unknown> = {}) => ({ display_name: '', has_logo: false, logo_width: 0, logo_height: 0, monthly_report_email: false, ...o })
const view = (o: { can_report: boolean; can_brand: boolean; branding?: Record<string, unknown> }) => ({ ...o, branding: branding(o.branding) })

interface Call {
  m: string
  url: string
  body?: unknown
  type?: string
}

function setup(opts: { role?: 'owner' | 'member'; view: ReturnType<typeof view>; retention?: number; onWrite?: (c: Call) => Promise<Response> | undefined }) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      const c: Call = { m: init?.method ?? 'GET', url, body: init?.body, type: (init?.headers as Record<string, string> | undefined)?.['Content-Type'] }
      calls.push(c)
      if (url.includes('/auth/me')) return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: opts.role ?? 'owner' }] })
      if (c.m !== 'GET') return opts.onWrite?.(c) ?? json(200, opts.view)
      if (url.endsWith('/branding')) return json(200, opts.view)
      if (url.endsWith('/usage')) return json(200, { usage: { plan: { code: 'x', name: 'X', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 60, history_retention_days: opts.retention ?? 90 }, monitors_used: 2, monitor_limit: 5, over_limit: false, next_plan: null } })
      if (url.includes('/monitors')) return json(200, { monitors: [{ id: 'm1', name: 'Client A' }, { id: 'm2', name: 'Client B' }], total: 2 })
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <AuthProvider>
        <ReportsPage />
      </AuthProvider>
    </MemoryRouter>,
  )
  return calls
}

const lastMonth = () => reportMonths(90)[1].value

describe('ReportsPage', () => {
  it('lists a PDF download per website, defaulting to the last full month', async () => {
    setup({ view: view({ can_report: true, can_brand: true }) })
    const links = await screen.findAllByRole('link', { name: 'Download PDF' })
    expect(links.map((l) => l.getAttribute('href'))).toEqual([
      `/api/v1/monitors/m1/report.pdf?month=${lastMonth()}`,
      `/api/v1/monitors/m2/report.pdf?month=${lastMonth()}`,
    ])
    const other = reportMonths(90)[0]
    await userEvent.selectOptions(screen.getByLabelText('Month'), other.value)
    expect(screen.getAllByRole('link', { name: 'Download PDF' })[0]).toHaveAttribute('href', `/api/v1/monitors/m1/report.pdf?month=${other.value}`)
  })

  it('only offers months the plan still keeps history for', async () => {
    setup({ view: view({ can_report: true, can_brand: false }), retention: 7 })
    const select = await screen.findByLabelText('Month')
    expect(within(select).getAllByRole('option')).toHaveLength(1)
  })

  it('explains the paid features instead of offering them on the free plan', async () => {
    setup({ view: view({ can_report: false, can_brand: false }) })
    expect(await screen.findByText(/PDF reports are included in the Pro and Agency plans/)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Download PDF' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Email me the reports every month')).toBeDisabled()
    expect(screen.getByLabelText('Name on reports')).toBeDisabled()
    expect(screen.getByLabelText('Logo')).toBeDisabled()
    expect(screen.getAllByRole('link', { name: 'See plans' }).length).toBeGreaterThan(0)
  })

  it('lets Pro download and email reports but says branding is an Agency feature', async () => {
    setup({ view: view({ can_report: true, can_brand: false }) })
    expect(await screen.findAllByRole('link', { name: 'Download PDF' })).toHaveLength(2)
    expect(screen.getByLabelText('Email me the reports every month')).toBeEnabled()
    expect(screen.getByText(/included in the Agency plan/)).toBeInTheDocument()
    expect(screen.getByLabelText('Name on reports')).toBeDisabled()
    expect(screen.getByLabelText('Logo')).toBeDisabled()
  })

  it('turns the monthly email on and off', async () => {
    const calls = setup({ view: view({ can_report: true, can_brand: true }) })
    const box = await screen.findByLabelText('Email me the reports every month')
    await userEvent.click(box)
    await waitFor(() => expect(calls.find((c) => c.m === 'PUT')).toBeTruthy())
    const put = calls.find((c) => c.m === 'PUT')!
    expect(put.url).toBe('/api/v1/branding')
    expect(JSON.parse(put.body as string)).toEqual({ monthly_report_email: true })
    expect(await screen.findByText('Monthly emails are on.')).toBeInTheDocument()
  })

  it('saves the name shown on reports', async () => {
    const calls = setup({ view: view({ can_report: true, can_brand: true, branding: { display_name: 'Old Name' } }) })
    const field = await screen.findByLabelText('Name on reports')
    expect(field).toHaveValue('Old Name')
    expect(screen.getByRole('button', { name: 'Save name' })).toBeDisabled()
    await userEvent.clear(field)
    await userEvent.type(field, 'Northwind Digital')
    await userEvent.click(screen.getByRole('button', { name: 'Save name' }))
    await waitFor(() => expect(calls.find((c) => c.m === 'PUT')).toBeTruthy())
    expect(JSON.parse(calls.find((c) => c.m === 'PUT')!.body as string)).toEqual({ display_name: 'Northwind Digital' })
  })

  it('sends the chosen logo file as the request body', async () => {
    const calls = setup({ view: view({ can_report: true, can_brand: true }) })
    const file = new File([new Uint8Array([137, 80, 78, 71])], 'logo.png', { type: 'image/png' })
    await userEvent.upload(await screen.findByLabelText('Logo'), file)
    await waitFor(() => expect(calls.find((c) => c.m === 'PUT')).toBeTruthy())
    const put = calls.find((c) => c.m === 'PUT')!
    expect(put.url).toBe('/api/v1/branding/logo')
    expect(put.body).toBe(file)
    expect(put.type).toBe('image/png')
    expect(await screen.findByText('Logo saved.')).toBeInTheDocument()
  })

  it('refuses a logo that is too large without sending it', async () => {
    const calls = setup({ view: view({ can_report: true, can_brand: true }) })
    const big = new File([new Uint8Array(600 * 1024)], 'big.png', { type: 'image/png' })
    await userEvent.upload(await screen.findByLabelText('Logo'), big)
    expect(await screen.findByText(/Logo is too large/)).toBeInTheDocument()
    expect(calls.some((c) => c.m === 'PUT')).toBe(false)
  })

  it('shows the server’s reason when a logo is refused', async () => {
    setup({
      view: view({ can_report: true, can_brand: true }),
      onWrite: () => json(400, { error: { code: 'VALIDATION_ERROR', message: 'One or more fields are invalid.', details: { fields: { logo: 'must be a PNG or JPEG image' } } } }),
    })
    await userEvent.upload(await screen.findByLabelText('Logo'), new File(['this is not really an image'], 'logo.png', { type: 'image/png' }))
    expect(await screen.findByText('Logo must be a PNG or JPEG image')).toBeInTheDocument()
  })

  it('shows and removes an existing logo', async () => {
    const calls = setup({ view: view({ can_report: true, can_brand: true, branding: { has_logo: true, logo_width: 300, logo_height: 90 } }) })
    const img = await screen.findByRole('img', { name: /Your logo \(300 × 90 pixels\)/ })
    expect(img.getAttribute('src')).toContain('/api/v1/branding/logo')
    await userEvent.click(screen.getByRole('button', { name: 'Remove logo' }))
    await waitFor(() => expect(calls.some((c) => c.m === 'DELETE' && c.url === '/api/v1/branding/logo')).toBe(true))
  })

  it('lets members look but not change', async () => {
    setup({ role: 'member', view: view({ can_report: true, can_brand: true }) })
    expect(await screen.findAllByRole('link', { name: 'Download PDF' })).toHaveLength(2)
    expect(screen.getByLabelText('Email me the reports every month')).toBeDisabled()
    expect(screen.getByLabelText('Name on reports')).toBeDisabled()
    expect(screen.getByLabelText('Logo')).toBeDisabled()
    expect(screen.getByText(/Only an owner of the workspace can change this/)).toBeInTheDocument()
  })
})
