import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Plan, Usage } from '../api'
import { StatusBadge } from '../components/StatusBadge'
import { UsagePanel } from './MonitorsPage'

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
    expect(screen.getByText(/Down/)).toBeInTheDocument()
    expect(screen.getByText(/Paused/)).toBeInTheDocument()
  })
})
