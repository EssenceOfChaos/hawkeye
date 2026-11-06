import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { DNS_RECORD_TYPES } from '../../api'
import { AuthProvider } from '../../auth'
import { PAGES } from '../../seo'
import { DocPage, DOCS, DocsIndexPage } from './DocsPages'

vi.mock('../../analytics', () => ({ trackEvent: vi.fn(), trackPageView: vi.fn(), openCookieSettings: vi.fn(), initAnalytics: vi.fn(), analyticsEnabled: () => false, useConsent: () => ({}) }))
vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{}', { status: 401, headers: { 'Content-Type': 'application/json' } }))))

const page = (node: React.ReactNode) => render(<MemoryRouter><AuthProvider>{node}</AuthProvider></MemoryRouter>)

describe('docs', () => {
  it('lists every article on the index', () => {
    page(<DocsIndexPage />)
    for (const d of DOCS) expect(screen.getByRole('heading', { level: 2, name: d.title })).toBeInTheDocument()
  })

  it.each(DOCS.map((d) => d.path))('%s has a title and is indexed', (path) => {
    page(<DocPage path={path} />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(DOCS.find((d) => d.path === path)!.title)
    expect(PAGES.find((p) => p.path === path)?.index).toBe(true)
  })

  it('marks only the current article in the menu, and gives every link a decorative icon', () => {
    render(<MemoryRouter initialEntries={['/docs/alerts']}><AuthProvider><DocPage path="/docs/alerts" /></AuthProvider></MemoryRouter>)
    const nav = screen.getByRole('navigation', { name: 'Documentation' })
    const links = within(nav).getAllByRole('link')
    expect(links.map((l) => l.textContent)).toEqual(DOCS.map((d) => d.title))
    expect(links.filter((l) => l.classList.contains('d-menu-active')).map((l) => l.textContent)).toEqual(['Alerts'])
    expect(within(nav).getByRole('link', { current: 'page' })).toHaveTextContent('Alerts')
    for (const l of links) expect(l.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('names every record type a DNS monitor accepts', () => {
    page(<DocPage path="/docs/dns-monitoring" />)
    for (const t of DNS_RECORD_TYPES) expect(screen.getAllByText(new RegExp(`\\b${t}\\b`)).length).toBeGreaterThan(0)
  })

  it('shows the heartbeat ping forms the API accepts', () => {
    page(<DocPage path="/docs/cron-job-monitoring" />)
    expect(screen.getAllByText(/\/fail/).length).toBeGreaterThan(0)
    expect(screen.getByText(/0 2 \* \* \* \/usr\/local\/bin\/backup\.sh; curl .*\/\$\?/)).toBeInTheDocument()
  })
})
