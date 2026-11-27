import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../AuthProvider'
import { IncidentsPage } from '../pages/IncidentsPage'
import { AnalysisUsage } from './AIAnalysis'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const incident = (o: object) => ({
  id: 'i1', monitor_id: 'm1', monitor_name: 'Shop', started_at: '2026-11-27T14:30:00Z', resolved_at: null, status: 'open',
  cause_category: 'http_status', summary: 'Received HTTP 502', duration_seconds: 600, has_snapshot: true, ...o,
})

const done = {
  status: 'done', category: 'cdn_or_proxy', likely_cause: 'Cloudflare can’t reach the origin server.', evidence: ['502 from Cloudflare', 'Server wait 2.4 s'],
  next_step: 'The origin server’s health and logs.', confidence: 'medium', model: 'claude-haiku-5-5', requested_at: '2026-11-27T14:31:00Z',
  finished_at: '2026-11-27T14:31:04Z',
}

function renderPage(o: { role?: 'owner' | 'member'; setting?: object; list?: unknown[]; analyses?: unknown[] }) {
  const calls: { method: string; url: string; body?: unknown }[] = []
  const analyses = [...(o.analyses ?? [])]
  let setting = { available: true, included: true, enabled: true, limit: 250, used: 3, model: 'Claude Haiku 5.5 by Anthropic', ...o.setting }
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(init.body as string) : undefined
    calls.push({ method, url, body })
    if (url.includes('/auth/me')) {
      return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: o.role ?? 'owner' }] })
    }
    if (url.endsWith('/ai-drafts')) return json(200, { available: false, included: false, enabled: false, model: '' })
    if (url.endsWith('/ai-analysis')) {
      if (method === 'PUT') setting = { ...setting, enabled: body.enabled }
      return json(200, setting)
    }
    if (url.endsWith('/analysis')) {
      if (method === 'POST') {
        setting = { ...setting, used: setting.used + 1 }
        return json(202, { analysis: { ...done, status: 'pending', category: null, likely_cause: null, evidence: [], next_step: null, confidence: null } })
      }
      const next = analyses.length > 1 ? analyses.shift() : analyses[0]
      return next ? json(200, { analysis: next }) : json(404, { error: { code: 'NOT_FOUND', message: 'none' } })
    }
    if (url.includes('/snapshot')) return json(404, { error: { code: 'NOT_FOUND', message: 'none' } })
    if (url.includes('/updates')) return json(200, { updates: [], status_pages: [] })
    return json(200, { incidents: o.list ?? [incident({})], total: 1 })
  }))
  render(<MemoryRouter><AuthProvider><IncidentsPage /></AuthProvider></MemoryRouter>)
  return calls
}

describe('AI incident analysis', () => {
  it('shows the likely cause, labeled as AI that may be wrong, with the evidence and the next step', async () => {
    renderPage({ list: [incident({ has_analysis: true })], analyses: [done] })
    await userEvent.click(await screen.findByRole('button', { name: 'What HawkEye saw' }))
    expect(await screen.findByText('Cloudflare can’t reach the origin server.')).toBeInTheDocument()
    expect(screen.getByText('AI analysis · may be wrong')).toBeInTheDocument()
    expect(screen.getByText('CDN or proxy:')).toBeInTheDocument()
    expect(screen.getByText('Server wait 2.4 s')).toBeInTheDocument()
    expect(screen.getByText(/The origin server’s health and logs/)).toBeInTheDocument()
    expect(screen.getByText(/Confidence: medium/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Analyze again' })).toBeEnabled()
  })

  it('asks for an analysis, says what it uses, and shows it once written', async () => {
    const calls = renderPage({ analyses: [{ ...done, status: 'running' }, done] })
    await userEvent.click(await screen.findByRole('button', { name: 'What HawkEye saw' }))
    expect(await screen.findByText('Uses one of the 247 analyses left this month.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Find the likely cause with AI' }))
    expect(await screen.findByText('Writing the analysis…')).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/incidents/i1/analysis'))).toBe(true)
    expect(await screen.findByText('Cloudflare can’t reach the origin server.', {}, { timeout: 8000 })).toBeInTheDocument()
  }, 12000)

  it('offers nothing to ask for when analysis is off, and says why an analysis ended without a cause', async () => {
    renderPage({ setting: { enabled: false } })
    await userEvent.click(await screen.findByRole('button', { name: 'What HawkEye saw' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: /likely cause/i })).not.toBeInTheDocument())

    vi.unstubAllGlobals()
    document.body.innerHTML = ''
    renderPage({ list: [incident({ has_analysis: true })], analyses: [{ ...done, status: 'over_limit', likely_cause: null, category: null, evidence: [] }],
      setting: { used: 250 } })
    await userEvent.click(await screen.findByRole('button', { name: 'What HawkEye saw' }))
    expect(await screen.findByText(/analyses were all used, so this incident wasn’t analyzed/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Analyze again' })).toBeDisabled()
  })

  it('lets the owner turn it on and says what is sent, never the address or the response', async () => {
    const calls = renderPage({ setting: { enabled: false } })
    const toggle = await screen.findByLabelText('Find the likely cause of outages with AI')
    expect(screen.getByText(/Never the monitor’s name or address, its credentials, or the response itself/)).toBeInTheDocument()
    await userEvent.click(toggle)
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.url.endsWith('/ai-analysis') && (c.body as { enabled: boolean }).enabled)).toBe(true))
  })

  it('shows members the analysis but not the switch', async () => {
    renderPage({ role: 'member', list: [incident({ has_analysis: true })], analyses: [done] })
    await userEvent.click(await screen.findByRole('button', { name: 'What HawkEye saw' }))
    expect(await screen.findByText('Cloudflare can’t reach the origin server.')).toBeInTheDocument()
    expect(screen.queryByLabelText(/likely cause of outages/)).not.toBeInTheDocument()
  })
})

describe('AI analysis usage ring', () => {
  const ring = () => screen.getByRole('progressbar', { name: 'AI analyses used this month' })

  it('shows how much of the month is used, for screen readers too', () => {
    const { container } = render(<MemoryRouter><AnalysisUsage state={{ used: 12, limit: 250 }} /></MemoryRouter>)
    expect(ring()).toHaveAttribute('aria-valuenow', '12')
    expect(ring()).toHaveAttribute('aria-valuemax', '250')
    expect(ring()).toHaveAttribute('aria-valuetext', '12 of 250 used')
    expect(ring().style.getPropertyValue('--d-value')).toBe('5')
    expect(screen.getByText(/238 left · starts again/)).toBeInTheDocument()
    expect(container.querySelector('.analysis-usage.ok')).not.toBeNull()
  })

  it('turns amber near the limit and red when used up, saying when it starts again and where to upgrade', () => {
    const { container, unmount } = render(<MemoryRouter><AnalysisUsage state={{ used: 210, limit: 250 }} /></MemoryRouter>)
    expect(container.querySelector('.analysis-usage.near')).not.toBeNull()
    unmount()
    const r = render(<MemoryRouter><AnalysisUsage state={{ used: 1000, limit: 1000 }} upgrade /></MemoryRouter>)
    expect(r.container.querySelector('.analysis-usage.full')).not.toBeNull()
    expect(ring().style.getPropertyValue('--d-value')).toBe('100')
    expect(screen.getByText(/All used: new outages aren’t analyzed until/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'See plans' })).toHaveAttribute('href', '/billing')
  })

  it('is on the owner’s switch only while analysis is on', async () => {
    renderPage({ setting: { enabled: true, used: 3 } })
    expect(await screen.findByRole('progressbar', { name: 'AI analyses used this month' })).toHaveAttribute('aria-valuenow', '3')
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
    renderPage({ setting: { enabled: false } })
    await screen.findByLabelText('Find the likely cause of outages with AI')
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })
})
