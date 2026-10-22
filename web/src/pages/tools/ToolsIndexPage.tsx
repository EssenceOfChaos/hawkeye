import { Link } from 'react-router'
import { useAuth } from '../../auth'
import { brand } from '../../brand'
import { CookieSettingsButton } from '../../components/CookieBanner'

const TOOLS = [
  { to: '/tools/is-website-down', title: 'Is My Website Down?', text: 'Check whether a website answers right now, with its HTTP status code and response time.' },
  { to: '/tools/ssl-certificate-checker', title: 'SSL Certificate Checker', text: 'Enter a domain to see when its SSL certificate expires, who issued it and whether browsers will trust it.' },
  { to: '/tools/uptime-sla-calculator', title: 'Uptime SLA Calculator', text: 'Turn 99%, 99.9% or 99.99% into the downtime it allows per day, week, month, quarter and year, or work out the uptime you delivered.' },
  { to: '/tools/downtime-cost-calculator', title: 'Downtime Cost Calculator', text: 'Estimate the revenue a website outage puts at risk from your daily revenue, trading hours and outage length.' },
]

export function ToolsIndexPage() {
  const { status } = useAuth()
  const signedIn = status === 'authenticated'
  return (
    <div className="legal tool">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logoWhite} alt="HawkEye" width={160} height={32} /></Link>
        <span className="spacer" />
        {signedIn ? <Link to="/dashboard">Dashboard</Link> : <Link to="/login">Log in</Link>}
      </header>
      <main>
        <h1>Free website monitoring tools</h1>
        <p className="tool-intro">Free tools from HawkEye for checking whether a site is up, checking certificates, planning uptime and understanding what downtime costs. No sign-up needed.</p>
        <ul className="tool-list">
          {TOOLS.map((t) => (
            <li key={t.to} className="card">
              <h2><Link to={t.to}>{t.title}</Link></h2>
              <p>{t.text}</p>
            </li>
          ))}
        </ul>
        <p className="hint">
          <Link to="/">Home</Link> · <Link to="/contact">Contact us</Link> · <Link to="/privacy">Privacy policy</Link> · <Link to="/terms">Terms of service</Link> · <CookieSettingsButton />
        </p>
      </main>
    </div>
  )
}
