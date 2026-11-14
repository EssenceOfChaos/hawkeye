import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../auth'
import { formatDuration, IncidentsPage } from './IncidentsPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const incident = (o: object) => ({
  id: 'i1', monitor_id: 'm1', monitor_name: 'Client Site', started_at: '2026-10-12T14:30:00Z', resolved_at: null,
  status: 'open', cause_category: 'timeout', summary: 'Request timed out', duration_seconds: 3700, ...o,
})

type Call = { method: string; url: string; body?: unknown }

function renderPage(list: unknown[], o: { role?: 'owner' | 'member'; pages?: unknown[]; updates?: unknown[]; aiDrafts?: { available: boolean; included: boolean; enabled: boolean } } = {}) {
  const calls: Call[] = []
  let updates = o.updates ?? []
  document.cookie = 'csrf_token=t; path=/'
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(init.body as string) : undefined
    calls.push({ method, url, body })
    if (url.includes('/auth/me')) {
      return json(200, { user: { id: 'u', email: 'me@example.com', email_verified_at: '2026-01-01T00:00:00Z' }, organizations: [{ id: 'o', name: 'Org', role: o.role ?? 'owner' }] })
    }
    if (url.endsWith('/ai-drafts')) {
      const state = { model: 'Claude Haiku 5.5 by Anthropic', ...(o.aiDrafts ?? { available: false, included: false, enabled: false }) }
      return json(200, method === 'PUT' ? { ...state, enabled: body.enabled } : state)
    }
    if (url.endsWith('/updates/draft')) return json(200, { message: `Draft for ${body.status}.` })
    if (url.includes('/updates')) {
      if (method === 'POST') {
        const u = { id: `u${updates.length + 1}`, incident_id: 'i1', status: body.status, message: body.message, author_email: 'me@example.com', created_at: '2026-10-12T15:00:00Z' }
        updates = [u, ...updates]
        return json(201, { update: u })
      }
      if (method === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
      return json(200, { updates, status_pages: o.pages ?? [] })
    }
    return json(200, { incidents: list, total: list.length })
  }))
  render(<MemoryRouter><AuthProvider><IncidentsPage /></AuthProvider></MemoryRouter>)
  return calls
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

  it('keeps the start time in one piece, so a narrow column never leaves AM or PM on a line of its own', async () => {
    renderPage([incident({})])
    const time = (await screen.findByRole('table')).querySelector('time[datetime="2026-10-12T14:30:00Z"]')!
    const parts = [...time.querySelectorAll('span')]
    expect(parts).toHaveLength(2)
    expect(parts.every((p) => p.classList.contains('nowrap'))).toBe(true)
    expect(parts[0].textContent).toBe(`${new Date('2026-10-12T14:30:00Z').toLocaleDateString()},`)
    expect(parts[1].textContent).toBe(new Date('2026-10-12T14:30:00Z').toLocaleTimeString()) // the whole time, with its AM or PM
  })

  it('lets the owner post an update, says where it appears, and suggests the next status', async () => {
    const calls = renderPage([incident({ update_count: 0 })], { pages: [{ title: 'Acme status', slug: 'acme' }] })
    await userEvent.click(await screen.findByRole('button', { name: 'Add update' }))
    expect(await screen.findByRole('link', { name: 'Acme status' })).toHaveAttribute('href', '/status/acme')
    expect(screen.getByLabelText('Status')).toHaveValue('investigating')
    await userEvent.type(screen.getByLabelText('Update'), 'We are looking into it.')
    await userEvent.click(screen.getByRole('button', { name: 'Post update' }))
    expect(await screen.findByText('We are looking into it.')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ status: 'investigating', message: 'We are looking into it.' })
    expect(screen.getByLabelText('Status')).toHaveValue('identified')
    expect(screen.getByRole('button', { name: '1 update' })).toHaveAttribute('aria-expanded', 'true')
  })

  it('fills in a draft for the chosen status, for the owner to edit and post', async () => {
    const calls = renderPage([incident({ update_count: 0 })], { aiDrafts: { available: true, included: true, enabled: true } })
    await userEvent.click(await screen.findByRole('button', { name: 'Add update' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Draft with AI' }))
    expect(screen.getByLabelText('Update')).toHaveValue('Draft for investigating.')
    expect(screen.getByText(/Written by AI from this incident’s details/)).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/updates'))).toBe(false) // nothing posted until the owner posts it
  })

  it('offers drafts only once the owner turns them on, and says what is sent', async () => {
    const calls = renderPage([incident({ update_count: 0 })], { aiDrafts: { available: true, included: true, enabled: false } })
    await userEvent.click(await screen.findByRole('button', { name: 'Add update' }))
    expect(await screen.findByLabelText('Update')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Draft with AI' })).not.toBeInTheDocument()
    expect(screen.getByText(/Never the monitor’s address/)).toBeInTheDocument()
    await userEvent.click(screen.getByLabelText('Draft incident updates with AI'))
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ enabled: true })
    expect(await screen.findByRole('button', { name: 'Draft with AI' })).toBeInTheDocument()
  })

  it('points Free plans at the plans that include drafts', async () => {
    renderPage([incident({})], { aiDrafts: { available: true, included: false, enabled: false } })
    expect(await screen.findByText(/On Pro and Agency, HawkEye can write a first draft/)).toBeInTheDocument()
  })

  it('warns when no published status page shows the incident', async () => {
    renderPage([incident({})])
    await userEvent.click(await screen.findByRole('button', { name: 'Add update' }))
    expect(await screen.findByText(/only your team sees these updates/)).toBeInTheDocument()
  })

  it('shows a member the updates but not the form', async () => {
    renderPage([incident({ update_count: 1 })], { role: 'member', updates: [{ id: 'u1', incident_id: 'i1', status: 'monitoring', message: 'A fix is out.', author_email: 'owner@example.com', created_at: '2026-10-12T15:00:00Z' }] })
    await userEvent.click(await screen.findByRole('button', { name: '1 update' }))
    expect(await screen.findByText('A fix is out.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Update')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Delete/ })).not.toBeInTheDocument()
    expect(screen.getByText(/Only the organization’s owner can post updates/)).toBeInTheDocument()
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
