import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MonitoringFlow } from './MonitoringFlow'

describe('MonitoringFlow', () => {
  it('describes the whole story to people who cannot see it', () => {
    render(<MonitoringFlow />)
    const fig = screen.getByRole('img', { name: 'How HawkEye confirms an outage and alerts you' })
    expect(fig).toHaveAccessibleDescription(/checks again before alerting, confirms the outage after consecutive failed checks/)
    expect(fig).toHaveAccessibleDescription(/sends a recovery alert/)
  })

  it('shows a single still frame without motion: an outage confirmed and alerts sent', () => {
    // jsdom has no matchMedia, which the page treats as reduced motion.
    const { container } = render(<MonitoringFlow />)
    const svg = container.querySelector('svg.flow')!
    expect(svg).toHaveAttribute('data-state', 'alerted')
    expect(svg).not.toHaveAttribute('data-running')
    expect(screen.getByText('Illustration, not live data.')).toBeInTheDocument()
  })

  it('names only what HawkEye monitors and the alert channels it has', () => {
    const { container } = render(<MonitoringFlow />)
    const text = container.textContent ?? ''
    for (const label of ['Website', 'API', 'Cron job', 'DNS record', 'SSL certificate', 'Email', 'Chat apps', 'Webhook']) expect(text).toContain(label)
    expect(text).toContain('Slack, Teams, Discord, Telegram')
    expect(text).not.toMatch(/healthchecks|live demo/i)
  })
})
