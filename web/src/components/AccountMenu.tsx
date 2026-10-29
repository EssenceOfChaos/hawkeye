import { useCallback, useRef, useState } from 'react'
import { NavLink, useLocation } from 'react-router'
import { useDismiss } from '../hooks'

/**
 * The person icon at the right of the header. It opens the account links (settings, team, billing) and Log out, so the main navigation keeps only
 * the pages used every day. While it is closed, hovering or focusing the icon shows the signed-in address in a tooltip; once open, the address is in the menu.
 *
 * It is a disclosure, not an ARIA menu: a plain button that shows or hides a group of ordinary links, so it works with a keyboard and a screen reader without
 * arrow-key handling. It closes on Escape (focus returns to the icon), on a click outside, when focus leaves it, and on any navigation.
 */
export function AccountMenu({ email, onLogout }: { email: string; onLogout: () => void }) {
  const { pathname } = useLocation()
  // The page it was opened on, not a flag: it counts as closed as soon as the address changes, with no effect to reset it.
  const [openAt, setOpenAt] = useState<string | null>(null)
  const open = openAt === pathname
  const root = useRef<HTMLDivElement>(null)
  const icon = useRef<HTMLButtonElement>(null)

  const close = useCallback((viaKeyboard: boolean) => {
    setOpenAt(null)
    if (viaKeyboard) icon.current?.focus()
  }, [])
  useDismiss(root, open, close)

  const link = ({ isActive }: { isActive: boolean }) => (isActive ? 'active' : undefined)

  return (
    <div
      className="account-menu"
      ref={root}
      onBlur={(e) => {
        if (open && !e.currentTarget.contains(e.relatedTarget as Node | null)) setOpenAt(null)
      }}
    >
      <span className="d-tooltip d-tooltip-bottom account-tip" data-tip={open ? '' : email}>
        <button
          ref={icon}
          type="button"
          className="who"
          aria-expanded={open}
          aria-controls="account-menu-panel"
          aria-label={`Account menu for ${email}`}
          onClick={() => setOpenAt(open ? null : pathname)}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M15.75 6a3.75 3.75 0 1 1-7.5 0 3.75 3.75 0 0 1 7.5 0ZM4.501 20.118a7.5 7.5 0 0 1 14.998 0A17.933 17.933 0 0 1 12 21.75c-2.676 0-5.216-.584-7.499-1.632Z" />
          </svg>
        </button>
      </span>
      {open && (
        // tabIndex keeps a click on the panel's own text inside the menu, so it does not count as focus leaving.
        <div id="account-menu-panel" className="account-panel" tabIndex={-1}>
          <p className="account-email">
            Signed in as <strong>{email}</strong>
          </p>
          <NavLink to="/account" className={link} onClick={() => setOpenAt(null)}>Account settings</NavLink>
          <NavLink to="/team" className={link} onClick={() => setOpenAt(null)}>Team</NavLink>
          <NavLink to="/billing" className={link} onClick={() => setOpenAt(null)}>Billing</NavLink>
          <button type="button" className="account-logout" onClick={onLogout}>Log out</button>
        </div>
      )}
    </div>
  )
}
