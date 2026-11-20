import { Link } from 'react-router'
import { brand, STATUS_URL } from '../brand'
import { useAuth } from '../auth'
import { CookieSettingsButton } from './CookieBanner'

// The copyright year, worked out once when the page loads rather than on every render (render code must not read the clock).
const year = new Date().getFullYear()

// Simple Icons paths (CC0), 24x24.
const SOCIALS = [
  { name: 'X', href: 'https://x.com/HawkEyeOnX', path: 'M14.234 10.162 22.977 0h-2.072l-7.591 8.824L7.251 0H.258l9.168 13.343L.258 24H2.33l8.016-9.318L16.749 24h6.993zm-2.837 3.299-.929-1.329L3.076 1.56h3.182l5.965 8.532.929 1.329 7.754 11.09h-3.182z' },
  { name: 'Bluesky', href: 'https://bsky.app/profile/hawkeye-observer.bsky.social', path: 'M12 10.8c-1.087-2.114-4.046-6.053-6.798-7.995C2.566.944 1.561 1.266.902 1.565.139 1.908 0 3.08 0 3.768c0 .69.378 5.65.624 6.479.815 2.736 3.713 3.66 6.383 3.364.136-.02.275-.039.415-.056-.138.022-.276.04-.415.056-3.912.58-7.387 2.005-2.83 7.078 5.013 5.19 6.87-1.113 7.823-4.308.953 3.195 2.05 9.271 7.733 4.308 4.267-4.308 1.172-6.498-2.74-7.078a8.741 8.741 0 0 1-.415-.056c.14.017.279.036.415.056 2.67.297 5.568-.628 6.383-3.364.246-.828.624-5.79.624-6.478 0-.69-.139-1.861-.902-2.206-.659-.298-1.664-.62-4.3 1.24C16.046 4.748 13.087 8.687 12 10.8Z' },
  { name: 'Facebook', href: 'https://www.facebook.com/people/HawkEye/61595561066921/', path: 'M9.101 23.691v-7.98H6.627v-3.667h2.474v-1.58c0-4.085 1.848-5.978 5.858-5.978.401 0 .955.042 1.468.103a8.68 8.68 0 0 1 1.141.195v3.325a8.623 8.623 0 0 0-.653-.036 26.805 26.805 0 0 0-.733-.009c-.707 0-1.259.096-1.675.309a1.686 1.686 0 0 0-.679.622c-.258.42-.374.995-.374 1.752v1.297h3.919l-.386 2.103-.287 1.564h-3.246v8.245C19.396 23.238 24 18.179 24 12.044c0-6.627-5.373-12-12-12s-12 5.373-12 12c0 5.628 3.874 10.35 9.101 11.647Z' },
  { name: 'LinkedIn', href: 'https://www.linkedin.com/company/hawkeye-observer/', path: 'M20.447 20.452h-3.554v-5.569c0-1.328-.027-3.037-1.852-3.037-1.853 0-2.136 1.445-2.136 2.939v5.667H9.351V9h3.414v1.561h.046c.477-.9 1.637-1.85 3.37-1.85 3.601 0 4.267 2.37 4.267 5.455v6.286zM5.337 7.433a2.062 2.062 0 0 1-2.063-2.065 2.064 2.064 0 1 1 2.063 2.065zm1.782 13.019H3.555V9h3.564v11.452zM22.225 0H1.771C.792 0 0 .774 0 1.729v20.542C0 23.227.792 24 1.771 24h20.451C23.2 24 24 23.227 24 22.271V1.729C24 .774 23.2 0 22.222 0h.003z' },
  { name: 'Reddit', href: 'https://www.reddit.com/r/HawkEye_Observer/', path: 'M12 0C5.373 0 0 5.373 0 12c0 3.314 1.343 6.314 3.515 8.485l-2.286 2.286C.775 23.225 1.097 24 1.738 24H12c6.627 0 12-5.373 12-12S18.627 0 12 0Zm4.388 3.199c1.104 0 1.999.895 1.999 1.999 0 1.105-.895 2-1.999 2-.946 0-1.739-.657-1.947-1.539v.002c-1.147.162-2.032 1.15-2.032 2.341v.007c1.776.067 3.4.567 4.686 1.363.473-.363 1.064-.58 1.707-.58 1.547 0 2.802 1.254 2.802 2.802 0 1.117-.655 2.081-1.601 2.531-.088 3.256-3.637 5.876-7.997 5.876-4.361 0-7.905-2.617-7.998-5.87-.954-.447-1.614-1.415-1.614-2.538 0-1.548 1.255-2.802 2.803-2.802.645 0 1.239.218 1.712.585 1.275-.79 2.881-1.291 4.64-1.365v-.01c0-1.663 1.263-3.034 2.88-3.207.188-.911.993-1.595 1.959-1.595Zm-8.085 8.376c-.784 0-1.459.78-1.506 1.797-.047 1.016.64 1.429 1.426 1.429.786 0 1.371-.369 1.418-1.385.047-1.017-.553-1.841-1.338-1.841Zm7.406 0c-.786 0-1.385.824-1.338 1.841.047 1.017.634 1.385 1.418 1.385.785 0 1.473-.413 1.426-1.429-.046-1.017-.721-1.797-1.506-1.797Zm-3.703 4.013c-.974 0-1.907.048-2.77.135-.147.015-.241.168-.183.305.483 1.154 1.622 1.964 2.953 1.964 1.33 0 2.47-.81 2.953-1.964.057-.137-.037-.29-.184-.305-.863-.087-1.795-.135-2.769-.135Z' },
  { name: 'Telegram', href: 'https://t.me/hawkeye_community', path: 'M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z' },
]

/**
 * The footer of the public marketing pages (home and About). On the home page the Features and Pricing links jump within the page; elsewhere they
 * open the home page at that section.
 */
export function SiteFooter({ onHome = false }: { onHome?: boolean }) {
  const { status } = useAuth()
  const signedIn = status === 'authenticated'
  return (
    <>
      <footer className="landing-footer d-footer">
        <aside>
          <img src={brand.logoWhite} alt="HawkEye" width={160} height={32} />
          <p>
            Your websites. Always in sight.
            <br />
            Website monitoring for people who need to know when something changes.
          </p>
        </aside>
        <nav aria-label="Product">
          <h6 className="d-footer-title">Product</h6>
          {onHome ? (
            <>
              <a className="d-link d-link-hover" href="#features-h">Features</a>
              <a className="d-link d-link-hover" href="#pricing-h">Pricing</a>
            </>
          ) : (
            <>
              <Link className="d-link d-link-hover" to="/#features-h">Features</Link>
              <Link className="d-link d-link-hover" to="/#pricing-h">Pricing</Link>
            </>
          )}
          <Link className="d-link d-link-hover" to="/docs">Docs</Link>
          <Link className="d-link d-link-hover" to="/tools">Free tools</Link>
          {signedIn ? (
            <Link className="d-link d-link-hover" to="/dashboard">Dashboard</Link>
          ) : (
            <>
              <Link className="d-link d-link-hover" to="/register">Sign up</Link>
              <Link className="d-link d-link-hover" to="/login">Log in</Link>
            </>
          )}
        </nav>
        <nav aria-label="Company">
          <h6 className="d-footer-title">Company</h6>
          <Link className="d-link d-link-hover" to="/about">About us</Link>
          <Link className="d-link d-link-hover" to="/contact">Contact us</Link>
          <a className="d-link d-link-hover" href={STATUS_URL}>HawkEye status</a>
          <Link className="d-link d-link-hover" to="/privacy">Privacy policy</Link>
          <Link className="d-link d-link-hover" to="/terms">Terms of service</Link>
          <CookieSettingsButton className="d-link d-link-hover" />
        </nav>
        <nav aria-label="Social media">
          <h6 className="d-footer-title">Follow</h6>
          <div className="social">
            {SOCIALS.map((s) => (
              <a key={s.name} href={s.href} target="_blank" rel="noopener noreferrer" className="d-btn d-btn-circle d-btn-ghost" aria-label={`HawkEye on ${s.name}`}>
                <svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"><path d={s.path} /></svg>
              </a>
            ))}
          </div>
        </nav>
      </footer>
      <p className="copyright">© {year} HawkEye. All rights reserved.</p>
    </>
  )
}
