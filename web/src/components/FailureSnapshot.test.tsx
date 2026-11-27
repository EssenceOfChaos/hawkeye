import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { FailureSnapshot as Snapshot } from '../api'
import { FailureSnapshot } from './FailureSnapshot'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const SNAP: Snapshot = {
  checked_at: '2026-11-27T09:00:00Z', region: 'eu-central', step_index: null, step_name: null,
  facts: { status_code: 502, content_type: 'text/html', size_bytes: 1200, server_addr: '198.51.100.9', final_url: 'https://api.example.com/health',
    headers: [{ name: 'Server', value: 'cloudflare' }, { name: 'X-Powered-By', value: '<script>alert(1)</script>' }],
    timings: { dns_ms: 12, connect_ms: 31, tls_ms: 48, wait_ms: 2410, redirects: 0 } },
  changes: [{ field: 'status', before: '200', after: '502' }, { field: 'header', name: 'Server', before: 'nginx', after: 'cloudflare' },
    { field: 'timing', name: 'wait', before: '180 ms', after: '2.4 s' }],
  locations: [{ region: 'eu-central', name: 'Amsterdam', status: 'down', error: 'Received HTTP 502', checked_at: '2026-11-27T09:00:00Z' },
    { region: 'us-east', name: 'New York', status: 'up', checked_at: '2026-11-27T09:00:00Z' }],
  has_body_excerpt: true, body_excerpt_cleared_at: null,
}

function setup(snapshot: Snapshot | null, body = '<h1>Bad gateway</h1>') {
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    calls.push(url)
    if (url.endsWith('/snapshot/body')) return json(200, { body: { text: body, truncated: false } })
    return snapshot ? json(200, { snapshot }) : json(404, { error: { code: 'NOT_FOUND', message: 'this incident has no failure snapshot' } })
  }))
  const r = render(<MemoryRouter><FailureSnapshot incidentID="i1" monitorID="m1" /></MemoryRouter>)
  return { calls, ...r }
}

describe('FailureSnapshot', () => {
  it('shows the response, what changed, each location and the timings', async () => {
    setup(SNAP)
    expect(await screen.findByRole('heading', { name: 'What HawkEye saw' })).toBeInTheDocument()
    expect(screen.getByText('HTTP 502 · text/html · 1.2 KB')).toBeInTheDocument()
    expect(screen.getByText('198.51.100.9')).toBeInTheDocument()
    expect(screen.getByText(/from Amsterdam/)).toBeInTheDocument()
    const changes = screen.getByRole('heading', { name: 'Changed since the last good check' }).nextElementSibling as HTMLElement
    expect(changes.textContent).toContain('Status: 200 → 502')
    expect(changes.textContent).toContain('Server: nginx → cloudflare')
    expect(changes.textContent).toContain('Server wait: 2.4 s (usually 180 ms)')
    expect(screen.getByText('Received HTTP 502')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: /Where the time went: .*Server wait 2,410 ms/ })).toBeInTheDocument()
  })

  it('shows header values and the response as text, never as markup', async () => {
    const { container } = setup(SNAP, '<img src=x onerror="alert(1)"><h1>Bad gateway</h1>')
    expect(await screen.findByText('<script>alert(1)</script>')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Show response' }))
    expect(await screen.findByLabelText('Failed response body')).toHaveTextContent('<img src=x onerror="alert(1)"><h1>Bad gateway</h1>')
    expect(container.querySelector('img, script')).toBeNull()
  })

  it('fetches the body only when asked', async () => {
    const { calls } = setup(SNAP)
    await screen.findByRole('heading', { name: 'What HawkEye saw' })
    expect(calls.some((u) => u.endsWith('/snapshot/body'))).toBe(false)
  })

  it('points to the setting when no excerpt was kept', async () => {
    setup({ ...SNAP, has_body_excerpt: false })
    expect(await screen.findByRole('link', { name: 'Keep the start of failed responses' })).toHaveAttribute('href', '/monitors/m1/edit')
    expect(screen.queryByRole('button', { name: 'Show response' })).toBeNull()
  })

  it('says when retention removed the excerpt', async () => {
    setup({ ...SNAP, has_body_excerpt: false, body_excerpt_cleared_at: '2026-11-01T00:00:00Z' })
    expect(await screen.findByText('The response was removed 30 days after the incident ended.')).toBeInTheDocument()
  })

  it('renders nothing for an incident without a snapshot', async () => {
    const { container } = setup(null)
    await vi.waitFor(() => expect(container.textContent).toBe(''))
  })
})
