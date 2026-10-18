import { useEffect, useRef } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'
import { brand } from '../brand'

const year = new Date().getFullYear()

/** True when the visitor can safely be sent back through history: they navigated inside the app, or arrived from this same site. */
function canGoBack(locationKey: string): boolean {
  if (locationKey !== 'default') return true // an earlier entry of this single-page app
  try {
    return Boolean(document.referrer) && new URL(document.referrer).origin === window.location.origin && window.history.length > 1
  } catch {
    return false
  }
}

/**
 * Shown for every address the app does not know. The web server answers these requests with HTTP 404 and this same page (deploy/Caddyfile),
 * so the status is real even though the content is rendered here; inside the app the router lands here for unknown paths.
 * Nothing on the page claims anything about system health: the diagnostics describe this request only.
 */
export function NotFoundPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const heading = useRef<HTMLHeadingElement>(null)

  useEffect(() => {
    const previousTitle = document.title
    document.title = 'Page Not Found (404) | HawkEye'
    const robots = document.createElement('meta')
    robots.name = 'robots'
    robots.content = 'noindex,follow'
    document.head.appendChild(robots)
    heading.current?.focus({ preventScroll: true }) // announce the change to screen reader users after an in-app navigation
    return () => {
      document.title = previousTitle
      robots.remove()
    }
  }, [])

  const goBack = () => {
    if (canGoBack(location.key)) navigate(-1)
    else navigate('/')
  }

  return (
    <div className="nf">
      <header className="nf-header">
        <Link to="/" aria-label="HawkEye home" className="nf-brand">
          <img src={brand.logoWhite} alt="HawkEye" width={176} height={34} />
        </Link>
        <div className="nf-status">
          <span className="nf-dot" aria-hidden="true" />
          NAVIGATION DIAGNOSTICS
        </div>
      </header>
      <main id="main" className="nf-main">
        <p className="nf-eyebrow">NAVIGATION ANOMALY DETECTED</p>
        <div className="nf-radar" aria-hidden="true">
          <div className="nf-circle nf-c1" />
          <div className="nf-circle nf-c2" />
          <div className="nf-circle nf-c3" />
          <div className="nf-axis nf-axis-x" />
          <div className="nf-axis nf-axis-y" />
          <div className="nf-sweep" />
          <span className="nf-number">404</span>
          <div className="nf-reticle" />
        </div>
        <h1 ref={heading} tabIndex={-1}>
          <span className="nf-sr">Error 404: page not found. </span>Beyond our field of view.
        </h1>
        <p className="nf-desc">
          Our systems are sharp. This URL isn&apos;t.
          <br />
          The page you&apos;re looking for may have moved, been renamed, or no longer exists. Let&apos;s get you back on target.
        </p>
        <nav className="nf-actions" aria-label="Recovery options">
          <Link className="nf-button nf-primary" to="/">
            <span aria-hidden="true">↗</span> Return to Homepage
          </Link>
          <button className="nf-button nf-secondary" type="button" onClick={goBack}>
            <span aria-hidden="true">←</span> Previous Page
          </button>
        </nav>
        <section className="nf-info" aria-label="Error diagnostics">
          <div>
            <small>ERROR CODE</small>
            <strong>HTTP 404 · Not Found</strong>
          </div>
          <div>
            <small>REQUEST STATUS</small>
            <strong className="nf-gold">● Page Not Found</strong>
          </div>
          <div>
            <small>RECOMMENDED ACTION</small>
            <strong>Recalibrate Navigation</strong>
          </div>
        </section>
      </main>
      <footer className="nf-footer">
        <span>© {year} HAWKEYE. ALL RIGHTS RESERVED.</span>
        <span>SEE MORE. STAY AHEAD.</span>
        <span>PRECISION THROUGH INTELLIGENCE</span>
      </footer>
    </div>
  )
}
