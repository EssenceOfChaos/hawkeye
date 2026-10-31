import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, NavLink, Outlet, useLocation } from 'react-router'
import { CookieSettingsButton } from './CookieBanner'
import { api, type BillingSubscription } from '../api'
import { hasPendingPurchase, reportPurchaseIfPaid } from '../purchaseConversion'
import { Alert } from './Alert'
import { useAuth } from '../auth'
import { useDismiss } from '../hooks'
import { AccountMenu } from './AccountMenu'
import { BrandMark } from './Brand'

export function Layout() {
  const { user, logout, refresh, organization, organizations, selectOrganization } = useAuth()
  const [resent, setResent] = useState(false)
  const [openIncidents, setOpenIncidents] = useState(0)
  const { pathname } = useLocation()
  // The phone menu counts as closed as soon as the address changes (see AccountMenu), so choosing a page closes it with no effect to reset it.
  const [navOpenAt, setNavOpenAt] = useState<string | null>(null)
  const navOpen = navOpenAt === pathname
  const header = useRef<HTMLElement>(null)
  const menuButton = useRef<HTMLButtonElement>(null)
  const closeNav = useCallback((viaKeyboard: boolean) => {
    setNavOpenAt(null)
    if (viaKeyboard) menuButton.current?.focus()
  }, [])
  useDismiss(header, navOpen, closeNav)

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

  // A purchase confirmed after the buyer left the billing page is still reported to Google Ads, the next time they open any page.
  useEffect(() => {
    if (!hasPendingPurchase()) return
    let live = true
    api<{ subscription: BillingSubscription }>('GET', '/billing/subscription')
      .then((r) => live && reportPurchaseIfPaid(r.subscription))
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
      {/* `multi-org` is for the width of the organization switcher: with it the links need more room, so they collapse behind the menu button sooner. */}
      <header className={organizations.length > 1 ? 'topbar multi-org' : 'topbar'} ref={header}>
        <Link to="/" className="brand" aria-label="HawkEye home">
          <BrandMark />
        </Link>
        <nav id="main-nav" aria-label="Main" className={navOpen ? 'open' : undefined}>
          <NavLink to="/dashboard">Dashboard</NavLink>
          <NavLink to="/monitors">Monitors</NavLink>
          <NavLink to="/incidents">
            <span className="d-indicator">
              {openIncidents > 0 && (
                <span className="d-indicator-item d-badge d-badge-error incident-count" aria-hidden="true">{openIncidents > 9 ? '9+' : openIncidents}</span>
              )}
              Incidents
            </span>
            {openIncidents > 0 && <span className="sr-only"> ({openIncidents} open)</span>}
          </NavLink>
          <NavLink to="/domains">Domains</NavLink>
          <NavLink to="/notifications">Alerts</NavLink>
          <NavLink to="/maintenance">Maintenance</NavLink>
          <NavLink to="/status-page">Status page</NavLink>
          <NavLink to="/reports">Reports</NavLink>
        </nav>
        <span className="spacer" />
        {/* One group, so when the header is too narrow its controls wrap together instead of one dropping to a line of its own. */}
        <div className="topbar-actions">
          {organizations.length > 1 && (
            <select className="org-switch" aria-label="Organization" value={organization?.id} onChange={(e) => selectOrganization(e.target.value)}>
              {organizations.map((o) => (
                <option key={o.id} value={o.id}>{o.name}</option>
              ))}
            </select>
          )}
          <AccountMenu email={user?.email ?? ''} onLogout={() => void logout()} />
          {/* Below the width where the links fit on one row the links are behind this button. It is hidden by CSS on wider screens. */}
          <button
            ref={menuButton}
            type="button"
            className="menu-toggle"
            aria-expanded={navOpen}
            aria-controls="main-nav"
            aria-label={openIncidents > 0 ? `Menu (${openIncidents} open incident${openIncidents === 1 ? '' : 's'})` : 'Menu'}
            onClick={() => setNavOpenAt(navOpen ? null : pathname)}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" aria-hidden="true">
              {navOpen ? <path d="M6 6l12 12M18 6L6 18" /> : <path d="M4 7h16M4 12h16M4 17h16" />}
            </svg>
            {openIncidents > 0 && !navOpen && <span className="d-badge d-badge-error incident-count menu-count" aria-hidden="true">{openIncidents > 9 ? '9+' : openIncidents}</span>}
          </button>
        </div>
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
      {/* Keyed by organization, so switching reloads every page's data for the one chosen rather than showing the last one's. */}
      <main key={organization?.id}>
        <Outlet />
      </main>
      <footer className="app-footer">
        <Link to="/contact">Contact</Link> · <Link to="/privacy">Privacy</Link> · <Link to="/terms">Terms</Link> · <CookieSettingsButton />
      </footer>
    </>
  )
}
