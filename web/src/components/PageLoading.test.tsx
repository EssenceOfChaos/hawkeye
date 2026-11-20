import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { PageLoading } from './PageLoading'

describe('PageLoading', () => {
  it('shows a large ring and still tells screen readers the page is loading', () => {
    const { container } = render(<PageLoading />)
    expect(screen.getByRole('status')).toHaveTextContent('Loading…')
    const ring = container.querySelector('.d-loading.d-loading-ring.d-loading-xl')
    expect(ring).not.toBeNull()
    expect(ring).toHaveAttribute('aria-hidden', 'true')
  })
})
