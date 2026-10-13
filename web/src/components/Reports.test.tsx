import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { RangeInfo, SeriesPoint, Uptime } from '../api'
import { RangePicker } from './RangePicker'
import { ResponseChart, withGaps } from './ResponseChart'
import { UptimeHero } from './Uptime'

const uptime = (o: Partial<Uptime> = {}): Uptime => ({
  from: '2026-10-11T00:00:00Z', to: '2026-10-12T00:00:00Z', clamped_to_retention: false, total_checks: 1440, successful_checks: 1439,
  failed_checks: 1, uptime_percent: 99.93, insufficient_data: false, min_checks: 5, avg_response_ms: 210, method: 'check_based', ...o,
})

const range = (o: Partial<RangeInfo> = {}): RangeInfo => ({ from: '2026-10-11T00:00:00Z', to: '2026-10-12T00:00:00Z', preset: '24h', clamped_to_retention: false, retention_days: 7, ...o })

const pt = (t: string, o: Partial<SeriesPoint> = {}): SeriesPoint => ({ t, avg_ms: 200, max_ms: 400, checks: 15, failed: 0, ...o })

describe('UptimeHero', () => {
  it('shows the percentage with its window, sample size and method', () => {
    render(<UptimeHero uptime={uptime()} label="Last 24 hours" />)
    expect(screen.getByText('99.93%')).toBeInTheDocument()
    expect(screen.getByText(/Check-based uptime · Last 24 hours/)).toBeInTheDocument()
    expect(screen.getByText(/1,439 of 1,440 completed checks succeeded · 1 failed/)).toBeInTheDocument()
  })

  it('shows no percentage when there is too little data', () => {
    render(<UptimeHero uptime={uptime({ uptime_percent: null, insufficient_data: true, total_checks: 3, successful_checks: 3, failed_checks: 0 })} label="Last 24 hours" />)
    expect(screen.queryByText(/%$/)).not.toBeInTheDocument()
    expect(screen.getByText(/Not enough data yet: 3 of 5 checks needed/)).toBeInTheDocument()
  })

  it('explains the calculation on demand', async () => {
    render(<UptimeHero uptime={uptime()} label="Last 24 hours" />)
    await userEvent.click(screen.getByText('How is this calculated?'))
    expect(screen.getByText(/successful checks ÷ completed checks × 100/)).toBeVisible()
    expect(screen.getByText(/paused, or when a check did not run/)).toBeInTheDocument()
  })
})

describe('RangePicker', () => {
  it('is a labelled radio group with the current range selected', async () => {
    const onChange = vi.fn()
    render(<RangePicker value="7d" options={['24h', '7d']} onChange={onChange} />)
    const group = screen.getByRole('radiogroup', { name: 'Time range' })
    expect(within(group).getByRole('radio', { name: 'Last 7 days' })).toBeChecked()
    await userEvent.click(within(group).getByRole('radio', { name: 'Last 24 hours' }))
    expect(onChange).toHaveBeenCalledWith('24h')
  })
})

describe('withGaps', () => {
  it('inserts an empty point where whole buckets are missing so lines break', () => {
    const data = withGaps([pt('2026-10-12T00:00:00Z'), pt('2026-10-12T00:15:00Z'), pt('2026-10-12T03:00:00Z')], 900)
    expect(data).toHaveLength(4)
    expect(data[2].avg).toBeNull()
    expect(data[2].t).toBe(new Date('2026-10-12T00:30:00Z').getTime())
  })
  it('leaves contiguous data untouched', () => {
    expect(withGaps([pt('2026-10-12T00:00:00Z'), pt('2026-10-12T00:15:00Z')], 900)).toHaveLength(2)
  })
})

describe('ResponseChart', () => {
  const points = [pt('2026-10-11T10:00:00Z'), pt('2026-10-11T10:15:00Z', { failed: 2, avg_ms: null, max_ms: null })]

  it('always offers a table view with every value', async () => {
    render(<ResponseChart title="Average response time" points={points} bucketSeconds={900} range={range()} />)
    await userEvent.click(screen.getByText('View as table'))
    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getByText('200 ms')).toBeInTheDocument()
    expect(within(table).getAllByText('—').length).toBeGreaterThan(0) // the failed-only bucket has no response time
  })

  it('summarises failures in text and the period granularity', () => {
    render(<ResponseChart title="Average response time" points={points} bucketSeconds={900} range={range()} />)
    expect(screen.getByText(/averages per 15 min/)).toBeInTheDocument()
    expect(screen.getByText(/2 failed checks in this period/)).toBeInTheDocument()
  })

  it('says when history was clamped to the plan', () => {
    render(<ResponseChart title="T" points={points} bucketSeconds={900} range={range({ clamped_to_retention: true })} />)
    expect(screen.getByRole('note')).toHaveTextContent('Showing 7 days: that is how much history your plan keeps.')
  })

  it('has an empty state and dims (rather than blanks) while reloading', () => {
    const { rerender, container } = render(<ResponseChart title="T" points={[]} bucketSeconds={900} range={range()} />)
    expect(screen.getByText('No checks have run in this period yet.')).toBeInTheDocument()
    rerender(<ResponseChart title="T" points={points} bucketSeconds={900} range={range()} loading />)
    const fig = container.querySelector('figure')!
    expect(fig).toHaveAttribute('aria-busy', 'true')
    expect(fig.style.opacity).toBe('0.55')
  })
})
