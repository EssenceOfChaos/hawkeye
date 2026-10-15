import { useState } from 'react'
import { Link, NavLink, Outlet } from 'react-router'
import { api } from '../api'
import { useAuth } from '../auth'
import { BrandMark } from './Brand'

export function Layout() {
  const { user, logout, refresh } = useAuth()
  const [resent, setResent] = useState(false)

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
          <NavLink to="/incidents">Incidents</NavLink>
          <NavLink to="/notifications">Alerts</NavLink>
          <NavLink to="/maintenance">Maintenance</NavLink>
          <NavLink to="/billing">Billing</NavLink>
        </nav>
        <span className="spacer" />
        <Link className="who" to="/account" aria-label="Account settings">{user?.email}</Link>
        <button onClick={() => void logout()}>Log out</button>
      </header>
      {user && !user.email_verified_at && (
        <div role="status" className="banner">
          Please verify your email address — we sent you a link.{' '}
          {resent ? (
            'A new link is on its way.'
          ) : (
            <button className="linklike" onClick={() => void resend()}>
              Resend email
            </button>
          )}
        </div>
      )}
      <main>
        <Outlet />
      </main>
      <footer className="app-footer">
        <Link to="/contact">Contact</Link> · <Link to="/privacy">Privacy</Link> · <Link to="/terms">Terms</Link>
      </footer>
    </>
  )
}
