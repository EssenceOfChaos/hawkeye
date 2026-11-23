import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { DnsChange, Monitor } from '../api'
import { DnsChanges } from './DnsChanges'
import { StatusBadge } from './StatusBadge'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const monitor = { id: 'm1', type: 'dns', enabled: true, current_status: 'up', dns: { record_type: 'MX', expected: null, change_alerts: true } } as unknown as Monitor

const OPEN: DnsChange = { id: 'c2', record_type: 'MX', before: ['mail.example.com'], after: ['mx.other.net'], summary: 'mail.example.com → mx.other.net',
  detected_at: '2026-11-22T15:14:00Z', status: 'open', closed_at: null, accepted_by: null }
const PAST: DnsChange = { id: 'c1', record_type: 'MX', before: ['mail.old.com'], after: ['mail.example.com'], summary: 'mail.old.com → mail.example.com',
  detected_at: '2026-11-20T09:00:00Z', status: 'accepted', closed_at: '2026-11-20T09:30:00Z', accepted_by: 'freddy@example.com' }

function setup(changes: DnsChange[], current: string[] | null) {
  document.cookie = 'csrf_token=t; path=/'
  let list = changes
  const posts: string[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    if (init?.method === 'POST') {
      posts.push(url)
      list = list.map((c) => (c.status === 'open' ? { ...c, status: 'accepted' as const, accepted_by: 'me@example.com' } : c))
      return json(200, { dns_changes: list, current: ['mx.other.net'] })
    }
    return json(200, { dns_changes: list, current })
  }))
  const onChanged = vi.fn()
  render(<DnsChanges monitor={monitor} onChanged={onChanged} />)
  return { posts, onChanged }
}

describe('DnsChanges', () => {
  it('shows an open change before and after, and accepts it as intended', async () => {
    const { posts, onChanged } = setup([OPEN, PAST], ['mx.other.net'])
    expect(await screen.findByText('MX record changed')).toBeInTheDocument()
    expect(screen.getByText('mail.example.com', { selector: 'dd' })).toBeInTheDocument()
    expect(screen.getByText('mx.other.net', { selector: 'dd' })).toBeInTheDocument()
    expect(screen.getByText(/Accepted by freddy@example.com/)).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'This change was intended' }))
    await waitFor(() => expect(screen.queryByText('MX record changed')).not.toBeInTheDocument())
    expect(posts).toEqual(['/api/v1/monitors/m1/dns-changes/c2/accept'])
    expect(onChanged).toHaveBeenCalled()
    expect(screen.getByText(/Accepted by me@example.com/)).toBeInTheDocument()
  })

  it('shows the current answer, and says so when there are no changes yet', async () => {
    setup([], ['mail.example.com'])
    expect(await screen.findByText('mail.example.com')).toBeInTheDocument()
    expect(screen.getByText(/No changes yet/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'This change was intended' })).not.toBeInTheDocument()
  })
})

describe('StatusBadge', () => {
  it('says a DNS monitor with an open change has changed', () => {
    render(<StatusBadge monitor={{ ...monitor, dns: { ...monitor.dns!, changed_since: '2026-11-22T15:14:00Z' } }} />)
    expect(screen.getByText('Changed')).toBeInTheDocument()
  })
})
