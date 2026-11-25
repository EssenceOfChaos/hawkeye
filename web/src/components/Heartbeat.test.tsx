import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Monitor } from '../api'
import { formatDateTime, formatPeriod, formatShortDateTime } from '../format'
import { pingExamples } from '../pingExamples'
import { PingLog, PingSetup } from './Heartbeat'
import { StatusBadge } from './StatusBadge'

describe('formatPeriod', () => {
  it('uses the largest whole unit', () => {
    expect(formatPeriod(86400)).toBe('1 day')
    expect(formatPeriod(7 * 86400)).toBe('7 days')
    expect(formatPeriod(3600)).toBe('1 hour')
    expect(formatPeriod(5400)).toBe('90 minutes')
    expect(formatPeriod(300)).toBe('5 minutes')
  })
})

describe('pingExamples', () => {
  it('times out, retries, and reports the exit status where it can', () => {
    const ex = pingExamples('https://h.test/ping/abc')
    expect(ex.every((x) => x.code.includes('https://h.test/ping/abc'))).toBe(true)
    expect(ex.find((x) => x.label.includes('exit status'))!.code).toMatch(/; curl .*--retry 5 .*\/ping\/abc\/\$\?$/)
  })
})

const heartbeat = (lastPing: string | null) =>
  ({ id: 'm1', type: 'heartbeat', name: 'Backup', heartbeat: { ping_token: 'tok_tok_tok_tok_tok_12', grace_seconds: 600, last_ping_at: lastPing, due_by: null } }) as unknown as Monitor

describe('PingSetup', () => {
  it('shows the full ping address on this site, and that nothing alerts before the first ping', () => {
    render(<MemoryRouter><PingSetup monitor={heartbeat(null)} onChanged={() => undefined} /></MemoryRouter>)
    expect(screen.getByRole('textbox', { name: 'Ping address' })).toHaveValue(`${window.location.origin}/ping/tok_tok_tok_tok_tok_12`)
    expect(screen.getByRole('note')).toHaveTextContent(/Waiting for the first ping.*does not alert/)
    expect(screen.getByRole('button', { name: 'Get a new ping address' })).toBeInTheDocument()
  })

  it('drops the waiting note once the job has pinged', () => {
    render(<MemoryRouter><PingSetup monitor={heartbeat('2026-11-03T02:00:00Z')} onChanged={() => undefined} /></MemoryRouter>)
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
  })
})

describe('PingLog', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('keeps the time on one line: a short date that never wraps, with the full date on hover', async () => {
    const at = '2026-11-25T17:22:51Z'
    const ping = { id: 'p1', received_at: at, kind: 'failure', exit_status: null, method: 'POST', source_ip: '203.0.113.7', user_agent: 'hawk_eye/0.1.0 (Elixir)', body: 'MyApp.Nightly failed on attempt 1 of 1', ignored: false }
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ pings: [ping] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))))
    render(<PingLog monitorID="m1" refresh={0} />)
    const time = await screen.findByText(formatShortDateTime(at))
    expect(time.tagName).toBe('TIME')
    expect(time).toHaveAttribute('title', formatDateTime(at))
    expect(time.closest('td')).toHaveClass('nowrap')
  })
})

describe('StatusBadge for heartbeats', () => {
  it('speaks of pings, not of being online', () => {
    render(
      <>
        <StatusBadge monitor={{ type: 'heartbeat', enabled: true, current_status: 'unknown' }} />
        <StatusBadge monitor={{ type: 'heartbeat', enabled: true, current_status: 'up' }} />
        <StatusBadge monitor={{ type: 'heartbeat', enabled: true, current_status: 'down' }} />
        <StatusBadge monitor={{ type: 'heartbeat', enabled: false, current_status: 'down' }} />
      </>,
    )
    expect(screen.getByText(/Waiting for first ping/)).toBeInTheDocument()
    expect(screen.getByText(/On time/)).toBeInTheDocument()
    expect(screen.getByText(/Failing/)).toBeInTheDocument()
    expect(screen.getByText(/Paused/)).toBeInTheDocument()
  })
})
