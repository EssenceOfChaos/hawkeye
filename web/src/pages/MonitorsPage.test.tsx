import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { Plan, TagSummary, Usage } from '../api'
import { StatusBadge } from '../components/StatusBadge'
import { TagList } from '../components/Tags'
import { BulkBar, UsagePanel } from './MonitorsPage'

const tags: TagSummary[] = [
  { id: 't1', name: 'Acme', color: '#1a73e8', monitor_count: 3 },
  { id: 't2', name: 'Beta', color: null, monitor_count: 0 },
]

describe('TagList', () => {
  it('shows each tag by name, so colour is never the only signal', () => {
    render(<TagList tags={tags} />)
    expect(screen.getByRole('list', { name: 'Tags' })).toHaveTextContent(/Acme.*Beta/)
  })

  it('renders nothing for an untagged monitor', () => {
    const { container } = render(<TagList tags={[]} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('BulkBar', () => {
  it('runs simple actions on the selection', () => {
    const onRun = vi.fn()
    render(<BulkBar count={3} tags={tags} minMinutes={5} onRun={onRun} onClear={() => undefined} />)
    expect(screen.getByText('3 monitors selected')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Pause' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(onRun.mock.calls).toEqual([['pause'], ['delete']])
  })

  it('adds a chosen tag and sets an interval in seconds', () => {
    const onRun = vi.fn()
    render(<BulkBar count={1} tags={tags} minMinutes={1} onRun={onRun} onClear={() => undefined} />)
    expect(screen.getByText('1 monitor selected')).toBeInTheDocument()
    const add = screen.getByRole('button', { name: 'Add' })
    expect(add).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Tag to add'), { target: { value: 't2' } })
    fireEvent.click(add)
    fireEvent.change(screen.getByLabelText('Check every (minutes)'), { target: { value: '10' } })
    fireEvent.click(screen.getByRole('button', { name: 'Set interval' }))
    expect(onRun.mock.calls).toEqual([['add_tags', { tag_ids: ['t2'] }], ['set_interval', { check_interval_seconds: 600 }]])
  })

  it('sets alert timing in bulk, sending only what was chosen', () => {
    const onRun = vi.fn()
    render(<BulkBar count={4} tags={tags} minMinutes={5} onRun={onRun} onClear={() => undefined} />)
    fireEvent.click(screen.getByRole('button', { name: 'Alert timing…' }))
    const apply = screen.getByRole('button', { name: 'Apply' })
    expect(apply).toBeDisabled() // both start at "keep"

    fireEvent.change(screen.getByLabelText('Alert'), { target: { value: '15' } })
    fireEvent.click(apply)
    expect(onRun).toHaveBeenLastCalledWith('set_alert_timing', { alert_delay_minutes: 15 })

    fireEvent.click(screen.getByRole('button', { name: 'Alert timing…' }))
    fireEvent.change(screen.getByLabelText('Quiet hours'), { target: { value: 'off' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(onRun).toHaveBeenLastCalledWith('set_alert_timing', { quiet_hours: null })

    fireEvent.click(screen.getByRole('button', { name: 'Alert timing…' }))
    fireEvent.change(screen.getByLabelText('Quiet hours'), { target: { value: 'set' } })
    fireEvent.change(screen.getByLabelText('Quiet hours start'), { target: { value: '21:30' } })
    fireEvent.change(screen.getByLabelText('Time zone'), { target: { value: 'Europe/London' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(onRun).toHaveBeenLastCalledWith('set_alert_timing', { quiet_hours: { start: '21:30', end: '07:00', timezone: 'Europe/London' } })
  })

  it('hides tag actions when the organization has no tags', () => {
    render(<BulkBar count={2} tags={[]} minMinutes={5} onRun={() => undefined} onClear={() => undefined} />)
    expect(screen.queryByLabelText('Tag to add')).not.toBeInTheDocument()
  })
})

const free = { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7 } satisfies Plan
const pro = { code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90 } satisfies Plan

const usage = (used: number): Usage => ({ plan: free, monitors_used: used, monitor_limit: 5, over_limit: used > 5, next_plan: pro })

describe('UsagePanel', () => {
  it('shows usage without upsell below the limit', () => {
    render(<UsagePanel usage={usage(2)} />)
    expect(screen.getByText('2')).toBeInTheDocument()
    expect(screen.queryByText(/includes 50 monitors/)).not.toBeInTheDocument()
  })

  it('explains the next plan when the limit is reached', () => {
    render(<UsagePanel usage={usage(5)} />)
    expect(screen.getByRole('status')).toHaveTextContent(/used all/)
    expect(screen.getByText(/Pro \(\$19\/month\) includes 50 monitors/)).toBeInTheDocument()
  })

  it('tells over-limit users how to recover and that nothing was deleted', () => {
    render(<UsagePanel usage={usage(8)} />)
    expect(screen.getByRole('alert')).toHaveTextContent(/Nothing was deleted.*remove 3/)
  })
})

describe('StatusBadge', () => {
  it('uses text, not colour alone', () => {
    render(
      <MemoryRouter>
        <StatusBadge monitor={{ enabled: true, current_status: 'down' }} />
        <StatusBadge monitor={{ enabled: false, current_status: 'up' }} />
      </MemoryRouter>,
    )
    expect(screen.getByText(/Offline/)).toBeInTheDocument()
    expect(screen.getByText(/Paused/)).toBeInTheDocument()
  })

  it('says Slow only for an online monitor with an open slowdown; offline and paused come first', () => {
    render(
      <MemoryRouter>
        <StatusBadge monitor={{ enabled: true, current_status: 'up', slow_since: '2026-11-02T09:15:00Z' }} />
        <StatusBadge monitor={{ enabled: true, current_status: 'down', slow_since: '2026-11-02T09:15:00Z' }} />
        <StatusBadge monitor={{ enabled: false, current_status: 'up', slow_since: '2026-11-02T09:15:00Z' }} />
        <StatusBadge monitor={{ enabled: true, current_status: 'up', slow_since: null }} />
      </MemoryRouter>,
    )
    expect(screen.getAllByText(/^\s*Slow$/)).toHaveLength(1)
    expect(screen.getByText(/Offline/)).toBeInTheDocument()
    expect(screen.getByText(/Paused/)).toBeInTheDocument()
    expect(screen.getByText(/Online/)).toBeInTheDocument()
  })
})
