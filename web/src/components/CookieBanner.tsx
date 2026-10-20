import { Link } from 'react-router'
import { openCookieSettings, setChoice, useCookieSettings } from '../analytics'

/** The cookie settings panel, opened from the "Cookie settings" footer links. Analytics is on by default; this is where a visitor turns it off. */
export function CookieBanner() {
  const { visible, enabled } = useCookieSettings()
  if (!visible) return null
  return (
    <div className="cookie-banner" role="dialog" aria-modal="false" aria-labelledby="cookie-banner-title">
      <div className="cookie-banner-text">
        <strong id="cookie-banner-title">Cookie settings</strong>
        <p>
          We always use the cookies needed to sign you in and keep your account secure. We also use Google Analytics cookies to see how the site is used. Analytics is currently <strong>{enabled ? 'on' : 'off'}</strong>.
          See our <Link to="/privacy">Privacy Policy</Link>.
        </p>
      </div>
      <div className="cookie-banner-actions">
        <button type="button" className="d-btn d-btn-primary" onClick={() => setChoice('off')}>Turn off analytics</button>
        <button type="button" className="d-btn d-btn-primary" onClick={() => setChoice('on')}>Keep analytics on</button>
      </div>
    </div>
  )
}

/** A footer link that opens the cookie settings. */
export function CookieSettingsButton({ className }: { className?: string }) {
  return (
    <button type="button" className={`link-button ${className ?? ''}`.trim()} onClick={() => openCookieSettings()}>Cookie settings</button>
  )
}
