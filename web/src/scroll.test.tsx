import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, MemoryRouter, useNavigate } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useScrollToTopOnNavigation } from './scroll'

function Page() {
  useScrollToTopOnNavigation()
  const navigate = useNavigate()
  return (
    <nav>
      <Link to="/tools">Free tools</Link>
      <Link to="/tools#list">Tools list</Link>
      <button onClick={() => navigate(-1)}>Back</button>
    </nav>
  )
}

let scrollTo: ReturnType<typeof vi.fn>
beforeEach(() => {
  scrollTo = vi.fn()
  vi.stubGlobal('scrollTo', scrollTo)
})
afterEach(() => vi.unstubAllGlobals())

describe('useScrollToTopOnNavigation', () => {
  it('does not move the page when it first loads', () => {
    render(<MemoryRouter><Page /></MemoryRouter>)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('starts the next page at the top when a link is followed', async () => {
    render(<MemoryRouter><Page /></MemoryRouter>)
    await userEvent.click(screen.getByRole('link', { name: 'Free tools' }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).toHaveBeenCalledWith(0, 0)
  })

  it('leaves a link to a #section to scroll itself', async () => {
    render(<MemoryRouter><Page /></MemoryRouter>)
    await userEvent.click(screen.getByRole('link', { name: 'Tools list' }))
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('leaves going back to the browser, which restores the earlier position', async () => {
    render(<MemoryRouter><Page /></MemoryRouter>)
    await userEvent.click(screen.getByRole('link', { name: 'Free tools' }))
    scrollTo.mockClear()
    await userEvent.click(screen.getByRole('button', { name: 'Back' }))
    expect(scrollTo).not.toHaveBeenCalled()
  })
})
