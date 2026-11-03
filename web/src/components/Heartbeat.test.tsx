import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Monitor } from '../api'
import { formatPeriod, pingExamples, PingSetup } from './Heartbeat'
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
