import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, MemoryRouter, Route, Routes, useNavigate } from 'react-router'
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

  it('scrolls to the section a link names on another page, which the browser does not do for an in-app link', async () => {
    const scrollIntoView = vi.fn()
    function Pricing() {
      useScrollToTopOnNavigation()
      return <h2 id="pricing-h" ref={(el) => { if (el) el.scrollIntoView = scrollIntoView }}>Pricing</h2>
    }
    render(
      <MemoryRouter initialEntries={['/about']}>
        <Routes>
          <Route path="/about" element={<Link to="/#pricing-h">Explore Pricing</Link>} />
          <Route path="/" element={<Pricing />} />
        </Routes>
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('link', { name: 'Explore Pricing' }))
    expect(scrollIntoView).toHaveBeenCalledTimes(1)
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
