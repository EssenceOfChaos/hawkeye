import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router'
import { useAuth } from '../../auth'
import { brand, STATUS_URL } from '../../brand'
import { CookieSettingsButton } from '../../components/CookieBanner'
import { DOCS } from './docs'

function DocsShell({ children }: { children: ReactNode }) {
  const { status } = useAuth()
  return (
    <div className="legal docs">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logoWhite} alt="HawkEye" width={160} height={32} /></Link>
        <Link to="/docs">Docs</Link>
        <span className="spacer" />
        {status === 'authenticated' ? <Link to="/dashboard">Dashboard</Link> : <Link to="/register">Start free</Link>}
      </header>
      <div className="docs-layout">
        <nav aria-label="Documentation" className="docs-nav">
          <ul className="d-menu">
            {DOCS.map((d) => (
              <li key={d.path}>
                <NavLink to={d.path} className={({ isActive }) => (isActive ? 'd-menu-active' : undefined)}>
                  <d.icon />
                  {d.title}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main>{children}</main>
      </div>
      <p className="hint">
        <Link to="/">Home</Link> · <Link to="/tools">Free tools</Link> · <Link to="/contact">Contact us</Link> · <a href={STATUS_URL}>HawkEye status</a> ·{' '}
        <Link to="/privacy">Privacy policy</Link> ·{' '}
        <Link to="/terms">Terms of service</Link> · <CookieSettingsButton />
      </p>
    </div>
  )
}

export function DocsIndexPage() {
  return (
    <DocsShell>
      <h1>HawkEye documentation</h1>
      <p className="tool-intro">How to monitor websites, APIs, scheduled jobs and DNS records with HawkEye, and how to get the right alerts to the right people.</p>
      <ul className="tool-list">
        {DOCS.map((d) => (
          <li key={d.path} className="card">
            <h2><Link to={d.path}>{d.title}</Link></h2>
            <p>{d.summary}</p>
          </li>
        ))}
      </ul>
      <p>Can’t find an answer? <Link to="/contact">Ask us</Link>.</p>
    </DocsShell>
  )
}

export function DocPage({ path }: { path: string }) {
  const doc = DOCS.find((d) => d.path === path)!
  return (
    <DocsShell>
      <article>
        <h1>{doc.title}</h1>
        <doc.body />
        <p className="hint">Questions or something missing? <Link to="/contact">Tell us</Link>.</p>
      </article>
    </DocsShell>
  )
}
