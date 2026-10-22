import { Link } from 'react-router'
import { trackEvent } from '../analytics'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { CookieSettingsButton } from './CookieBanner'

/**
 * The frame around a free tool: public header and footer, the page heading, and the call to action to sign up. `tool` names the tool in
 * analytics (tool_cta_click). The call to action says only what HawkEye does today.
 */
export function ToolShell({ tool, title, intro, cta, children }: {
  tool: string
  title: string
  intro: string
  cta: { heading: string; text: string }
  children: React.ReactNode
}) {
  const { status } = useAuth()
  const signedIn = status === 'authenticated'
  return (
    <div className="legal tool">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logoWhite} alt="HawkEye" width={160} height={32} /></Link>
        <span className="spacer" />
        <Link to="/tools">Free tools</Link>
        {signedIn ? <Link to="/dashboard">Dashboard</Link> : <Link to="/login">Log in</Link>}
      </header>
      <main>
        <h1>{title}</h1>
        <p className="tool-intro">{intro}</p>
        {children}
        <section className="card tool-cta" aria-labelledby="tool-cta-h">
          <h2 id="tool-cta-h">{cta.heading}</h2>
          <p>{cta.text}</p>
          <p>
            <Link className="button primary" to={signedIn ? '/dashboard' : '/register'} onClick={() => trackEvent('tool_cta_click', { tool })}>
              {signedIn ? 'Open dashboard' : 'Start monitoring free'}
            </Link>
          </p>
        </section>
        <p className="hint">
          <Link to="/">Home</Link> · <Link to="/tools">Free tools</Link> · <Link to="/contact">Contact us</Link> · <Link to="/privacy">Privacy policy</Link> ·{' '}
          <Link to="/terms">Terms of service</Link> · <CookieSettingsButton />
        </p>
      </main>
    </div>
  )
}
