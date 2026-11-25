import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { StatusBadge } from './StatusBadge'

describe('StatusBadge', () => {
  it('says a DNS record is resolving or failing, not that a website is online or offline', () => {
    const { rerender } = render(<StatusBadge monitor={{ enabled: true, current_status: 'up', type: 'dns' }} />)
    expect(screen.getByText('Resolving')).toBeInTheDocument()
    rerender(<StatusBadge monitor={{ enabled: true, current_status: 'down', type: 'dns' }} />)
    expect(screen.getByText('Failing')).toBeInTheDocument()
    expect(screen.queryByText('Offline')).not.toBeInTheDocument()
  })

  it('still shows a website as online or offline', () => {
    render(<StatusBadge monitor={{ enabled: true, current_status: 'down', type: 'http' }} />)
    expect(screen.getByText('Offline')).toBeInTheDocument()
  })

  it('says a monitor is flapping while it keeps going down and coming back, and pausing still wins', () => {
    const { rerender } = render(<StatusBadge monitor={{ enabled: true, current_status: 'down', flapping_since: '2026-11-24T09:00:00Z' }} />)
    expect(screen.getByText('Flapping')).toBeInTheDocument()
    rerender(<StatusBadge monitor={{ enabled: false, current_status: 'down', flapping_since: '2026-11-24T09:00:00Z' }} />)
    expect(screen.getByText('Paused')).toBeInTheDocument()
  })
})
