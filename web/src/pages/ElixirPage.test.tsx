import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../AuthProvider'
import { ElixirPage, HAWK_EYE_GITHUB } from './ElixirPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function renderPage() {
  vi.stubGlobal('fetch', vi.fn(() => json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })))
  return render(
    <MemoryRouter initialEntries={['/elixir']}>
      <AuthProvider>
        <ElixirPage />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('ElixirPage', () => {
  it('has one main heading and the sections in order', () => {
    renderPage()
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Know when an Oban job fails. Or never runs.')
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'Three steps. No changes to your workers.',
      'Alerts on what matters, not on every retry.',
      'Small package. Careful defaults.',
      'Wrap any scheduled job.',
      'Your jobs run while you sleep.We’ll notice when they don’t.',
    ])
  })

  it('shows the install and config snippets with copy buttons, and links to the package', () => {
    renderPage()
    expect(screen.getByText(/\{:hawk_eye, "~> 0\.1"\}/)).toBeInTheDocument()
    expect(screen.getByText(/MyApp\.Workers\.NightlyReport => System\.get_env/)).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Copy code' })).toHaveLength(3)
    expect(screen.getByRole('link', { name: 'View on GitHub' })).toHaveAttribute('href', HAWK_EYE_GITHUB)
    expect(screen.getAllByRole('link', { name: 'Create Your Free Account' })[0]).toHaveAttribute('href', '/register')
  })

  it('says retries are not reported and that a job that never runs still alerts', () => {
    renderPage()
    const rows = screen.getAllByRole('row').map((r) => r.textContent)
    expect(rows).toContain('Failed, and Oban will retry itNothing: a transient error wakes nobody')
    expect(rows).toContain('Never ran at allNothing, so HawkEye alerts when the deadline passes')
  })

  it('keeps the error text in the ping log, never promising it in the alert', () => {
    renderPage()
    expect(screen.getByText(/shown in the monitor’s ping log/)).toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/error[^.]*in the alert/i)
  })
})
