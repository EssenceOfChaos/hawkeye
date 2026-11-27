import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Plan } from '../api'
import { PlanComparison } from './PlanComparison'

const free: Plan = { code: 'free', name: 'Free', monthly_price_cents: 0, max_monitors: 5, minimum_check_interval_seconds: 300, history_retention_days: 7, max_domains: 3, max_team_members: 1, max_regions: 1, max_status_pages: 1, max_workflows: 1, max_workflow_steps: 3 }
const pro: Plan = {
  code: 'pro', name: 'Pro', monthly_price_cents: 1900, max_monitors: 50, minimum_check_interval_seconds: 60, history_retention_days: 90, max_domains: 25, max_team_members: 1,
  max_regions: 3, max_status_pages: 3, max_workflows: 5, max_workflow_steps: 10, maintenance_enabled: true, reports_enabled: true, api_write_enabled: true, ai_drafts_enabled: true, status_page_branding: true,
}
const agency: Plan = {
  ...pro, code: 'agency', name: 'Agency', monthly_price_cents: 5900, max_monitors: 200, minimum_check_interval_seconds: 30, history_retention_days: 365, max_domains: 100, max_team_members: 5,
  max_regions: 5, max_status_pages: 50, max_workflows: 50, branded_reports_enabled: true, status_page_white_label: true, max_status_page_domains: 50,
}

const cells = (label: string) => [...screen.getByRole('rowheader', { name: label }).closest('tr')!.querySelectorAll('td')].map((td) => td.textContent)

describe('plan comparison table', () => {
  it('shows each plan’s real limits from the plans table', () => {
    render(<PlanComparison plans={[free, pro, agency]} />)
    expect([...document.querySelectorAll('thead th')].map((h) => h.textContent)).toEqual(['Free', 'Pro', 'Agency'])
    expect(cells('Price per month')).toEqual(['$0', '$19', '$59'])
    expect(cells('Monitors')).toEqual(['5', '50', '200'])
    expect(cells('Fastest check interval')).toEqual(['5 minutes', '1 minute', '30 seconds'])
    expect(cells('History kept')).toEqual(['7 days', '90 days', '365 days'])
    expect(cells('Check locations per monitor')).toEqual(['1', '3', '5'])
    expect(cells('Team members')).toEqual(['1', '1', '5'])
    expect(cells('Multi-step API checks')).toEqual(['1, up to 3 steps', '5, up to 10 steps', '50, up to 10 steps'])
    expect(cells('API and Claude (MCP) access')).toEqual(['Read and add monitors', 'Read and make changes', 'Read and make changes'])
  })

  it('reads yes and no aloud instead of symbols', () => {
    render(<PlanComparison plans={[free, pro, agency]} />)
    expect(cells('Maintenance windows')).toEqual(['–No', '✓Yes', '✓Yes'])
    expect(cells('Reports with your name and logo')).toEqual(['–No', '–No', '✓Yes'])
    expect(cells('On your clients’ own domains')).toEqual(['–No', '–No', '50'])
  })

  it('leaves out rows that are the same on every plan, and says once what every plan includes', () => {
    render(<PlanComparison plans={[{ ...pro, code: 'a', name: 'A' }, { ...pro, code: 'b', name: 'B', max_monitors: 60 }]} />)
    expect(screen.getByRole('rowheader', { name: 'Monitors' })).toBeInTheDocument()
    expect(screen.queryByRole('rowheader', { name: 'Maintenance windows' })).not.toBeInTheDocument()
    expect(screen.queryByRole('rowgroup', { name: /Status pages/ })).not.toBeInTheDocument()
    const included = screen.getByRole('heading', { name: 'Every plan includes' }).parentElement!
    expect(within(included).getByText('Alerts that show what changed: status, headers and timings')).toBeInTheDocument()
  })

  it('can sit behind a Compare plans disclosure', () => {
    const { container } = render(<PlanComparison plans={[free, pro]} collapsed />)
    expect(container.querySelector('details summary')?.textContent).toBe('Compare plans')
  })
})
