import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { timingsLine } from '../format'
import { TimingBar } from './TimingBar'

const avg = { dns_ms: 10, connect_ms: 30, tls_ms: 60, wait_ms: 880, other_ms: 20, checks: 1440 }

describe('TimingBar', () => {
  it('splits the average check into its phases, in order, with a legend and a table view', () => {
    const { container } = render(<TimingBar timings={avg} />)
    const bar = screen.getByRole('img')
    expect(bar).toHaveAccessibleName(/DNS lookup 10 ms \(1%\), Connect 30 ms \(3%\), TLS handshake 60 ms \(6%\), Server wait 880 ms \(88%\), Other 20 ms \(2%\)/)
    expect(screen.getByText(/average of 1,440 successful checks, 1,000 ms in all/)).toBeInTheDocument()
    // Only a block wide enough prints its share.
    const printed = [...container.querySelectorAll('.timing-part span')].map((s) => s.textContent)
    expect(printed).toEqual(['88%'])
    const legend = [...container.querySelectorAll('.timing-legend li')].map((li) => li.textContent)
    expect(legend).toEqual(['DNS lookup 10 ms', 'Connect 30 ms', 'TLS handshake 60 ms', 'Server wait 880 ms', 'Other 20 ms'])
    expect(screen.getByRole('table', { name: 'Where the time goes, on average' })).toBeInTheDocument()
  })

  it('explains a phase on hover or keyboard focus', () => {
    const { container } = render(<TimingBar timings={avg} />)
    const tip = container.querySelector('.timing-tip')!
    fireEvent.focus(screen.getByLabelText('Server wait: 880 ms, 88%'))
    expect(tip).toHaveTextContent('Server wait: 880 ms (88%). Time to first byte')
    fireEvent.blur(screen.getByLabelText('Server wait: 880 ms, 88%'))
    expect(tip).not.toHaveTextContent('Server wait')
  })

  it('leaves out phases that took no time, and "other" when it is unknown', () => {
    const { container } = render(<TimingBar timings={{ ...avg, tls_ms: 0, other_ms: null }} />)
    const legend = [...container.querySelectorAll('.timing-legend li')].map((li) => li.textContent)
    expect(legend).toEqual(['DNS lookup 10 ms', 'Connect 30 ms', 'Server wait 880 ms'])
  })

  it('shows nothing without any time measured', () => {
    const { container } = render(<TimingBar timings={{ dns_ms: 0, connect_ms: 0, tls_ms: 0, wait_ms: 0, other_ms: null, checks: 3 }} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('timingsLine', () => {
  it('describes one check', () => {
    // Non-breaking spaces inside each part: a line wraps only at the separators.
    const plain = (s: string) => s.replaceAll('\u00a0', '_')
    expect(plain(timingsLine({ dns_ms: 12, connect_ms: 31, tls_ms: 48, wait_ms: 1410, redirects: 1 }))).toBe(
      'DNS_12_ms · connect_31_ms · TLS_48_ms · server_1,410_ms · 1_redirect')
    expect(plain(timingsLine({ dns_ms: 0, connect_ms: 1, tls_ms: 0, wait_ms: 5, redirects: 0 }))).toBe('DNS_0_ms · connect_1_ms · server_5_ms')
  })
})
