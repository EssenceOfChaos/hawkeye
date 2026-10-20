import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation } from 'react-router'
import { CookieSettingsButton } from './CookieBanner'
import { api } from '../api'
import { Alert } from './Alert'
import { useAuth } from '../auth'
import { BrandMark } from './Brand'

export function Layout() {
  const { user, logout, refresh } = useAuth()
  const [resent, setResent] = useState(false)
  const [openIncidents, setOpenIncidents] = useState(0)
  const { pathname } = useLocation()

  // The badge on "Incidents" is refreshed on every navigation; a failure just leaves the last count in place.
  useEffect(() => {
    let live = true
    api<{ total: number }>('GET', '/incidents?status=open&limit=1')
      .then((r) => live && setOpenIncidents(r.total))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [pathname])

  async function resend() {
    await api('POST', '/auth/resend-verification')
    setResent(true)
    void refresh()
  }

  return (
    <>
      <header className="topbar">
        <Link to="/" className="brand" aria-label="HawkEye home">
          <BrandMark />
        </Link>
        <nav aria-label="Main">
          <NavLink to="/dashboard">Dashboard</NavLink>
          <NavLink to="/monitors">Monitors</NavLink>
          <NavLink to="/incidents">
            <span className="d-indicator">
              {openIncidents > 0 && (
                <span className="d-indicator-item d-badge d-badge-error d-badge-xs" aria-hidden="true">{openIncidents > 9 ? '9+' : openIncidents}</span>
              )}
              Incidents
            </span>
            {openIncidents > 0 && <span className="sr-only"> ({openIncidents} open)</span>}
          </NavLink>
          <NavLink to="/notifications">Alerts</NavLink>
          <NavLink to="/maintenance">Maintenance</NavLink>
          <NavLink to="/status-page">Status page</NavLink>
          <NavLink to="/billing">Billing</NavLink>
        </nav>
        <span className="spacer" />
        <Link className="who" to="/account" aria-label="Account settings">{user?.email}</Link>
        <button onClick={() => void logout()}>Log out</button>
      </header>
      {user && !user.email_verified_at && (
        <Alert tone="warning" role="status">
          Please verify your email address — we sent you a link.{' '}
          {resent ? (
            'A new link is on its way.'
          ) : (
            <button className="linklike" onClick={() => void resend()}>
              Resend email
            </button>
          )}
        </Alert>
      )}
      <main>
        <Outlet />
      </main>
      <footer className="app-footer">
        <Link to="/contact">Contact</Link> · <Link to="/privacy">Privacy</Link> · <Link to="/terms">Terms</Link> · <CookieSettingsButton />
      </footer>
    </>
  )
}
