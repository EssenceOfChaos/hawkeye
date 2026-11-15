import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { PublicStatus } from '../api'
import { StatusNotFound } from '../statusLoad'
import { StatusView } from './StatusView'

const page: PublicStatus = {
  title: 'Acme status', description: '', overall: 'operational', history_days: 7, generated_at: '2026-10-15T12:00:00Z',
  monitors: [], incidents: [], branding: { powered_by: true },
}

// The view as the custom-domain entry (src/status-main.tsx) uses it: no router around it.
describe('StatusView on a customer’s own domain', () => {
  it('links "Powered by HawkEye" to HawkEye itself, not to the customer’s domain', async () => {
    const load = () => Promise.resolve({ ...page, home_url: 'https://hawkeye.observer' })
    render(<StatusView load={load} />)
    expect(await screen.findByRole('link', { name: 'HawkEye' })).toHaveAttribute('href', 'https://hawkeye.observer')
  })

  it('ignores a home address that is not a web address', async () => {
    const load = () => Promise.resolve({ ...page, home_url: 'javascript:alert(1)' })
    render(<StatusView load={load} />)
    expect(await screen.findByRole('link', { name: 'HawkEye' })).toHaveAttribute('href', '/')
  })

  it('says "not found" for a domain that serves nothing, and "try again" when loading failed', async () => {
    const missing = () => Promise.reject(new StatusNotFound())
    const { unmount } = render(<StatusView load={missing} />)
    expect(await screen.findByText('Status page not found')).toBeInTheDocument()
    unmount()
    const broken = () => Promise.reject(new Error('status 502'))
    render(<StatusView load={broken} />)
    expect(await screen.findByText(/could not be loaded/)).toBeInTheDocument()
  })
})
