import { Link } from 'react-router'
import { brand } from '../brand'

function Shell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="legal">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logo} alt="HawkEye" width={120} height={43} /></Link>
      </header>
      <main>
        <p role="note" className="placeholder-banner">
          <strong>PLACEHOLDER — DRAFT FOR LEGAL REVIEW.</strong> This text describes how the service is built today so a lawyer can review it. It is not legal advice and
          is not yet a binding policy. Items in [square brackets] must be completed before launch.
        </p>
        <h1>{title}</h1>
        {children}
        <p className="hint"><Link to="/privacy">Privacy policy</Link> · <Link to="/terms">Terms of service</Link> · <Link to="/">Home</Link></p>
      </main>
    </div>
  )
}

export function PrivacyPage() {
  return (
    <Shell title="Privacy policy">
      <p>Last updated: [date]. Controller: [legal entity name, address]. Contact: [privacy contact email].</p>

      <h2>What we collect and why</h2>
      <ul>
        <li><strong>Account:</strong> your email address and a password hash (Argon2id; we never store your password). Used to run your account and send security emails.</li>
        <li><strong>Monitoring data:</strong> the website addresses you ask us to watch, the settings you choose, and the results of each check (time, HTTP status, response time, a short error category). We do <em>not</em> store the content of monitored pages.</li>
        <li><strong>Alert recipients:</strong> email addresses you add to receive outage alerts. An address must be confirmed by its owner before it receives alerts.</li>
        <li><strong>Billing:</strong> payments are handled by Stripe. We store your plan and subscription status and Stripe’s customer reference; we never see or store card numbers.</li>
        <li><strong>Technical data:</strong> server logs with request details (such as IP address and request ID) for security and diagnosing problems; short-lived rate-limit counters (stored hashed). We use only essential cookies (a session cookie and an anti-forgery cookie); no advertising or analytics cookies.</li>
      </ul>

      <h2>How long we keep it</h2>
      <ul>
        <li>Check history and resolved incidents: <strong>7 days</strong> (Free), <strong>90 days</strong> (Pro), <strong>365 days</strong> (Agency), then deleted automatically.</li>
        <li>Monitors you delete: removed after 30 days. Expired sessions and one-time links: removed automatically.</li>
        <li>Payment-provider webhook records: 90 days (180 if they failed). [Log retention period: …]. [Backup retention: …].</li>
        <li>When you delete your account everything above is deleted immediately (backups age out on their normal schedule), and your Stripe customer record is deleted. Stripe keeps transaction records it is legally required to keep.</li>
      </ul>

      <h2>Who we share data with (sub-processors)</h2>
      <p>[Hosting provider, region]; Stripe (payments); [email delivery provider] (alerts and account emails); [error-tracking provider, if enabled]. [Add data-processing agreements and transfer mechanisms.]</p>

      <h2>Your rights</h2>
      <p>You can download your data and delete your account yourself under <Link to="/account">Account</Link>. [Describe rights of access, correction, portability, objection and complaint, appropriate to your jurisdictions; add how to contact us.]</p>

      <h2>Security</h2>
      <p>Passwords are hashed, connections use TLS, each customer’s data is isolated, and outbound checks are restricted so they cannot reach private networks. See [security page] for details. [Breach notification commitment.]</p>

      <h2>Children and changes</h2>
      <p>[Minimum age statement.] [How we notify you of changes to this policy.]</p>
    </Shell>
  )
}

export function TermsPage() {
  return (
    <Shell title="Terms of service">
      <p>Last updated: [date]. Provider: [legal entity name, address]. Governing law and venue: [jurisdiction].</p>

      <h2>The service</h2>
      <p>HawkEye checks websites you add and emails you when they appear to be down or recovered. [Describe plans and limits; they are enforced by the service and shown on the pricing page.]</p>

      <h2>Acceptable use</h2>
      <ul>
        <li>Only add websites and APIs that you own or are authorized to monitor.</li>
        <li>Do not use the service to probe, scan or attack other systems, or to reach private or internal networks. Checks are restricted accordingly.</li>
        <li>Do not try to bypass plan limits, rate limits or security controls. [Add suspension and termination rights.]</li>
      </ul>

      <h2>No guarantee of detection or delivery</h2>
      <p>Monitoring and alert emails are provided on a best-effort basis. Checks run at intervals, an outage can occur between checks, and email can be delayed or fail. [Service-level statement or explicit absence of one.] Do not rely on the service as your only safeguard for safety-critical systems.</p>

      <h2>Plans, payment and cancellation</h2>
      <p>Paid plans renew monthly through Stripe until cancelled. You can cancel at any time and keep access until the end of the paid period. If a plan ends, your existing monitors and data are kept, but you cannot add more than the Free plan allows. [Refund policy, taxes, price changes.]</p>

      <h2>Your data</h2>
      <p>You own your data. You can export it and delete your account at any time (see the <Link to="/privacy">privacy policy</Link>).</p>

      <h2>Liability and changes</h2>
      <p>[Warranty disclaimer, limitation of liability, indemnity.] [How terms may change and how you will be told.] [Contact for legal notices.]</p>
    </Shell>
  )
}
