import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { describe, expect, it } from 'vitest'
import { NotFoundPage } from './NotFoundPage'

function renderNotFound(entries: string[], index?: number) {
  return render(
    <MemoryRouter initialEntries={entries} initialIndex={index}>
      <Routes>
        <Route path="/" element={<p>home page</p>} />
        <Route path="/earlier" element={<p>earlier page</p>} />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('404 page', () => {
  it('shows the approved copy with one heading and a labelled main landmark', () => {
    renderNotFound(['/nope'])
    expect(screen.getByRole('heading', { level: 1, name: /Beyond our field of view\./ })).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Error 404: page not found.')
    expect(screen.getByRole('main')).toBeInTheDocument()
    expect(screen.getByText('NAVIGATION ANOMALY DETECTED')).toBeInTheDocument()
    expect(screen.getByText(/Our systems are sharp\. This URL isn't\./)).toBeInTheDocument()
    expect(screen.getByText('HTTP 404 · Not Found')).toBeInTheDocument()
    expect(screen.getByText('SEE MORE. STAY AHEAD.')).toBeInTheDocument()
    expect(screen.getByText('PRECISION THROUGH INTELLIGENCE')).toBeInTheDocument()
  })

  it('never claims anything about system health', () => {
    renderNotFound(['/nope'])
    expect(document.body.textContent).not.toMatch(/operational|all systems|uptime|online/i)
  })

  it('uses the official logo as the home link and hides the decorative radar from assistive technology', () => {
    const { container } = renderNotFound(['/nope'])
    const home = screen.getByRole('link', { name: 'HawkEye home' })
    expect(home).toHaveAttribute('href', '/')
    expect(home.querySelector('img')).toHaveAttribute('alt', 'HawkEye')
    expect(container.querySelector('.nf-radar')).toHaveAttribute('aria-hidden', 'true')
  })

  it('returns to the homepage', () => {
    renderNotFound(['/nope'])
    expect(screen.getByRole('link', { name: /Return to Homepage/ })).toHaveAttribute('href', '/')
  })

  it('goes back through history when the visitor navigated inside the app', async () => {
    renderNotFound(['/earlier', '/nope'], 1)
    await userEvent.click(screen.getByRole('button', { name: /Previous Page/ }))
    expect(await screen.findByText('earlier page')).toBeInTheDocument()
  })

  it('goes home instead when the page was opened directly (no same-site history to return to)', async () => {
    renderNotFound(['/nope'])
    await userEvent.click(screen.getByRole('button', { name: /Previous Page/ }))
    expect(await screen.findByText('home page')).toBeInTheDocument()
  })

  it('both recovery controls are reachable and operable with the keyboard', async () => {
    renderNotFound(['/nope'])
    const user = userEvent.setup()
    expect(screen.getByRole('heading', { level: 1 })).toHaveFocus() // moved here on arrival, for screen reader users
    await user.tab()
    expect(screen.getByRole('link', { name: /Return to Homepage/ })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: /Previous Page/ })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(await screen.findByText('home page')).toBeInTheDocument()
  })

  it('asks search engines not to index it, and restores the page title and robots tag when it goes away', () => {
    document.title = 'before'
    const { unmount } = renderNotFound(['/nope'])
    expect(document.title).toBe('Page Not Found (404) | HawkEye')
    expect(document.querySelector('meta[name="robots"]')).toHaveAttribute('content', 'noindex,follow')
    unmount()
    expect(document.title).toBe('before')
    expect(document.querySelector('meta[name="robots"]')).toBeNull()
  })
})
