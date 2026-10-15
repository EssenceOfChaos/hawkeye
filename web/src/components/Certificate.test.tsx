import { act, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SSLInfo } from '../api'
import { CertBadge, CertificateCard, CertificatesPanel } from './Certificate'

const NOW = new Date('2026-10-14T12:00:00Z')
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(NOW)
})
afterEach(() => vi.useRealTimers())

const days = (n: number) => new Date(NOW.getTime() + n * 86_400_000).toISOString()
const ssl = (n: number): SSLInfo => ({ expires_at: days(n), checked_at: NOW.toISOString(), days_remaining: Math.floor(n), status: n < 0 ? 'expired' : n <= 30 ? 'expiring' : 'valid' })
const https = 'https://example.com'

describe('CertBadge', () => {
  it('shows days left with a tone, and never colour alone', () => {
    const { container, rerender } = render(<CertBadge monitor={{ url: https, ssl: ssl(45) }} />)
    expect(container).toHaveTextContent('45 days')
    expect(container.querySelector('.cert-ok')).toBeTruthy()
    expect(container).toHaveTextContent(/Valid/)
    rerender(<CertBadge monitor={{ url: https, ssl: ssl(6) }} />)
    expect(container).toHaveTextContent('6 days')
    expect(container.querySelector('.cert-soon')).toBeTruthy()
    rerender(<CertBadge monitor={{ url: https, ssl: ssl(-2) }} />)
    expect(container).toHaveTextContent('Expired')
    expect(container.querySelector('.cert-expired')).toBeTruthy()
  })

  it('handles monitors without a certificate yet, and plain HTTP', () => {
    const { container, rerender } = render(<CertBadge monitor={{ url: https, ssl: null }} />)
    expect(container).toHaveTextContent('Not checked yet')
    rerender(<CertBadge monitor={{ url: 'http://example.com', ssl: null }} />)
    expect(container).toHaveTextContent('No SSL')
  })

  it('says "Under a day" instead of 0 days', () => {
    const { container } = render(<CertBadge monitor={{ url: https, ssl: ssl(0.4) }} />)
    expect(container).toHaveTextContent('Under a day')
  })
})

describe('CertificateCard', () => {
  it('shows a countdown and which warning emails are due', () => {
    render(<CertificateCard monitor={{ url: https, ssl: ssl(6.5) }} />)
    expect(screen.getByRole('timer')).toHaveAccessibleName('6 days, 12 hours and 0 minutes until the certificate expires')
    expect(screen.getByText('Expiring soon')).toBeInTheDocument()
    const items = screen.getAllByRole('listitem')
    // 30, 14 and 7 days have passed; 1 day is upcoming; the last item is the expiry itself.
    expect(within(items[0]).getByText(/sent, or due/)).toBeInTheDocument()
    expect(within(items[2]).getByText(/sent, or due/)).toBeInTheDocument()
    expect(within(items[3]).getByText('upcoming')).toBeInTheDocument()
    expect(items[0]).toHaveClass('reached')
    expect(items[3]).not.toHaveClass('reached')
  })

  it('a healthy certificate has no warnings reached', () => {
    render(<CertificateCard monitor={{ url: https, ssl: ssl(80) }} />)
    expect(screen.getByText('Valid')).toBeInTheDocument()
    expect(screen.getAllByRole('listitem').some((li) => li.classList.contains('reached'))).toBe(false)
  })

  it('reports an expired certificate plainly', () => {
    render(<CertificateCard monitor={{ url: https, ssl: ssl(-3) }} />)
    expect(screen.getAllByText('Expired').length).toBeGreaterThan(0)
    expect(screen.getByText(/Expired 3 days ago/)).toBeInTheDocument()
    expect(screen.queryByRole('timer')).not.toBeInTheDocument()
  })

  it('explains itself when there is nothing to show', () => {
    const { rerender } = render(<CertificateCard monitor={{ url: https, ssl: null }} />)
    expect(screen.getByRole('status')).toHaveTextContent(/next check/)
    rerender(<CertificateCard monitor={{ url: 'http://example.com', ssl: null }} />)
    expect(screen.getByText(/uses http:\/\//)).toBeInTheDocument()
  })

  it('keeps counting down while the page is open', () => {
    render(<CertificateCard monitor={{ url: https, ssl: ssl(1.0005) }} />)
    expect(screen.getByRole('timer')).toHaveAccessibleName(/1 days, 0 hours and 0 minutes/)
    act(() => {
      vi.advanceTimersByTime(5 * 60_000)
    })
    expect(screen.getByRole('timer')).toHaveAccessibleName(/0 days, 23 hours and 5[0-9] minutes/)
  })
})

describe('CertificatesPanel', () => {
  const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

  it('lists certificates needing attention, linking to each monitor', () => {
    wrap(
      <CertificatesPanel tracked={4} certificates={[
        { monitor_id: 'a', monitor_name: 'Old API', expires_at: days(-2), days_remaining: -2, status: 'expired' },
        { monitor_id: 'b', monitor_name: 'Blog', expires_at: days(6), days_remaining: 6, status: 'expiring' },
      ]} />,
    )
    expect(screen.getByRole('link', { name: 'Old API' })).toHaveAttribute('href', '/monitors/a')
    expect(screen.getByText('Expired')).toBeInTheDocument()
    expect(screen.getByText('6 days')).toBeInTheDocument()
  })

  it('says everything is fine when nothing is close, and hides itself when nothing is tracked', () => {
    const { container, rerender } = wrap(<CertificatesPanel tracked={3} certificates={[]} />)
    expect(container).toHaveTextContent('All 3 tracked certificates have more than 30 days left')
    rerender(<MemoryRouter><CertificatesPanel tracked={0} certificates={[]} /></MemoryRouter>)
    expect(container).toBeEmptyDOMElement()
  })
})
