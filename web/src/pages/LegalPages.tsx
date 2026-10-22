import { Link } from 'react-router'
import { brand } from '../brand'
import { CookieSettingsButton } from '../components/CookieBanner'

function Shell({ title, children, draft = true }: { title: string; children: React.ReactNode; draft?: boolean }) {
  return (
    <div className="legal">
      <header className="legal-header">
        <Link to="/" aria-label="HawkEye home"><img src={brand.logoWhite} alt="HawkEye" width={160} height={32} /></Link>
      </header>
      <main>
        {draft && (
          <p role="note" className="placeholder-banner">
          <strong>PLACEHOLDER — DRAFT FOR LEGAL REVIEW.</strong> This text describes how the service is built today so a lawyer can review it. It is not legal advice and
          is not yet a binding policy. Items in [square brackets] must be completed before launch.
        </p>
        )}
        <h1>{title}</h1>
        {children}
        <p className="hint"><Link to="/contact">Contact us</Link> · <Link to="/privacy">Privacy policy</Link> · <Link to="/terms">Terms of service</Link> · <CookieSettingsButton /> · <Link to="/">Home</Link></p>
      </main>
    </div>
  )
}

const SUPPORT = 'support@hawkeye.observer'

export function PrivacyPage() {
  return (
    <Shell title="HawkEye Privacy Policy" draft={false}>
      <p><strong>Effective date:</strong> October 14, 2026<br /><strong>Last updated:</strong> October 15, 2026</p>
      <p>
        HawkEye (“HawkEye,” “we,” “us,” or “our”) respects your privacy. This Privacy Policy explains how we collect, use, disclose, and retain information when you
        visit our website, create an account, or use our website-monitoring service (together, the “Service”).
      </p>

      <h2>1. Who We Are</h2>
      <p>The Service is operated by <strong>Hawkeye</strong>, a limited liability company.</p>
      <ul>
        <li><strong>Legal business name:</strong> Hawkeye</li>
        <li><strong>Privacy and support contact:</strong> <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> (or use our <Link to="/contact">contact form</Link>)</li>
        <li><strong>Website:</strong> https://hawkeye.observer</li>
      </ul>
      <p>For privacy inquiries, requests concerning your personal information, or questions about this Policy, contact us at <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a>.</p>

      <h2>2. Information We Collect</h2>
      <p>The information we collect depends on how you use the Service.</p>

      <h3>A. Account and contact information</h3>
      <p>When you create or manage an account, we collect or generate:</p>
      <ul>
        <li>Your email address.</li>
        <li>Your password, which we never store. We keep only a one-way hash made with the Argon2id algorithm.</li>
        <li>Whether your email address has been verified, and the name of the workspace (organization) created for your account.</li>
        <li>Your subscription plan, account status, and related service records.</li>
        <li>Alert destinations you add: email addresses you ask us to notify and their confirmation status, and Slack or webhook URLs. We store those URLs and any webhook signing secret encrypted, and show you only the host name afterwards.</li>
        <li>Communications you send to us. If you use our contact form we receive the name, email address, subject, message, and optional website address you enter and, if you are signed in, your account identifier and plan.</li>
      </ul>
      <p>
        We do not ask for your name when you create an account. HawkEye runs its own sign-in system: we do not use a third-party sign-in provider, and we do not currently offer
        multi-factor authentication.
      </p>

      <h3>B. Website-monitoring information</h3>
      <p>When you configure a website or endpoint for monitoring, we process information needed to operate the Service, which includes:</p>
      <ul>
        <li>Monitor name and URL, and your monitoring settings (such as check interval and accepted status codes).</li>
        <li>For API checks, the request you configure: the request method, custom headers, a request body, authentication credentials (a username and password, a bearer token, or an API key), and the response checks you define.</li>
        <li>For each check: the time, the HTTP status code, the response time, whether your response checks passed, and a short error category if it failed. To evaluate response checks we read the response (up to the first 1 MB) while the check runs; we do not store its content.</li>
        <li>Incident and recovery records, alert settings, and records of the notifications we sent.</li>
        <li>Maintenance windows you schedule: their names, times, repeat settings, and the monitors they cover. Checks made during a window are recorded and marked as such.</li>
        <li>For HTTPS monitors, the expiry date of the SSL certificate your website presents, and when we last read it. We do not store the certificate itself or any other detail from it.</li>
        <li>Domain names: the registered domain of each monitor’s web address (for example example.com for https://www.example.com) and any domain you add yourself, the registration expiry date a public domain registry reports for it, and when we last looked it up.</li>
        <li>Usage counts needed to enforce your plan’s limits.</li>
      </ul>
      <p>
        A monitored URL may itself contain information identifying a person or account. Do not include passwords, secret tokens, private credentials, or other sensitive
        information in monitor URLs, custom headers, or request bodies. Put credentials only in the monitor’s Authentication fields: they are stored encrypted, are never shown again
        after you save them, are not included in data exports, and are sent only to the website or API you configure (they are removed from any request redirected to a different host). Custom headers and request bodies are not encrypted, are visible to the other members of your organization, and are included in your data export. HawkEye is designed to monitor websites and endpoints that you are authorized to monitor; do not configure monitoring in a way that exposes
        information you are not authorized to share.
      </p>

      <h3>Information you choose to publish</h3>
      <p>
        If you create a public status page, anyone who has its address can see the page title and description you enter, the names you give the monitors you add to it, each one’s current state (up, down, or under maintenance),
        daily and overall uptime percentages, and the start and end times of recent incidents. We do not show monitored web addresses, error messages, or the causes of incidents on the page. The page is not visible until you
        publish it, and you can unpublish or delete it at any time. Do not put personal or confidential information in the title, description, or monitor names. Public status pages may be cached by us for a short time (under a minute)
        and, once published, may be copied or indexed by others, which we cannot control.
      </p>

      <h3>C. Technical and security information</h3>
      <p>
        When you access the Service, our systems process technical information such as your IP address, browser type, timestamps, the pages and API paths requested, response
        codes, and security events. We keep these in server logs to run and secure the Service and diagnose problems. Our web server removes one-time tokens from logged addresses and does
        not log cookies, authorization headers, or the page you came from. Server logs are kept on our hosting server and rotated automatically by size, so they cover roughly the most
        recent days to weeks depending on traffic.
      </p>
      <p>
        To limit abuse we keep short-lived rate-limit counters. They are keyed by a one-way hash of the IP address or email address involved and are deleted within 24 hours.
      </p>
      <p>
        We use <strong>Google Analytics</strong> (provided by Google LLC) to understand how people use our website, for example which pages are visited and from what kind of device. Google receives the
        page address (without any query string or fragment, so one-time links and tokens are not shared; the one exception is campaign tags such as <code>utm_source</code> and advertising click identifiers, which tell us which of our ads or links brought you here and contain no personal information), the page title, whether you created an account (an event with no name or email attached), your IP address, browser and device information, and an identifier stored in a cookie, and it processes
        them for us under its own terms. We turn off Google’s advertising features and ad personalization, and we do not send your name, email address, or monitoring data to Google. You can turn analytics off at any time (see Section E), and we also treat a Global Privacy Control signal from your browser as a choice to opt out. When analytics is off, no Google script is loaded and nothing is sent to Google. We do not use an advertising or
        error-tracking service. If that changes, we will update this Policy first.
      </p>

      <h3>D. Subscription and payment information</h3>
      <p>
        We use <strong>Stripe</strong> to process paid subscriptions. When you subscribe, you enter your payment details on a page hosted by Stripe, and Stripe processes your name,
        email address, billing details, payment method information, transaction details, and fraud-prevention signals. <strong>Your card details are sent to Stripe and never pass through or
        are stored on our servers.</strong>
      </p>
      <p>
        We receive and store from Stripe your Stripe customer and subscription identifiers, plan, subscription status, renewal and cancellation dates, and related billing events. We keep copies of
        Stripe’s event notifications (which can include billing contact details) for a limited time, described in Section 5. Stripe processes information under its own terms and
        privacy notice, and acts as an independent controller for some activities, such as fraud prevention and compliance with financial regulation.
      </p>

      <h3>E. Cookies and similar technologies</h3>
      <p>HawkEye always uses the cookies it needs to work. Google Analytics cookies, which measure how the site is used, are set by default, and you can turn them off at any time (see below). We do not use advertising cookies or pixels, and we host our fonts ourselves so your browser does not contact a font provider.</p>
      <table>
        <thead><tr><th>Cookie</th><th>Purpose</th><th>Category</th><th>Lifetime</th></tr></thead>
        <tbody>
          <tr><td><code>hawkeye_session</code></td><td>Keeps you signed in. HTTP-only, SameSite=Lax, and Secure.</td><td>Strictly necessary</td><td>Up to 14 days, or until you sign out</td></tr>
          <tr><td><code>csrf_token</code></td><td>Protects your account against cross-site request forgery.</td><td>Strictly necessary</td><td>Up to 24 hours</td></tr>
          <tr><td><code>hawkeye_analytics</code></td><td>Remembers your analytics choice (on or off) if you use “Cookie settings.”</td><td>Strictly necessary</td><td>1 year</td></tr>
          <tr><td><code>_ga</code>, <code>_ga_ZNT8EMTFGY</code></td><td>Set by Google Analytics, unless you turn analytics off, to tell visitors and sessions apart and measure site usage.</td><td>Analytics (opt-out)</td><td>Up to 2 years</td></tr>
        </tbody>
      </table>
      <p>
        The sign-in, security, and cookie-choice cookies are strictly necessary to provide the Service you ask for. The Google Analytics cookies are not, so you are free to refuse them: use the “Cookie settings” link at the bottom
        of our pages and choose “Turn off analytics,” which stops Google Analytics and deletes its cookies. If your browser sends a Global Privacy Control signal, we treat that as turning analytics off. You can also block or delete cookies in your browser settings, or install Google’s <a href="https://tools.google.com/dlpage/gaoptout" target="_blank" rel="noopener noreferrer">Analytics opt-out browser add-on</a>; the Service works the same without them. Google’s handling of the data is described in its
        <a href="https://policies.google.com/technologies/partner-sites" target="_blank" rel="noopener noreferrer">“How Google uses information from sites or apps that use our services”</a> page. When you pay, Stripe’s hosted checkout page may set its own cookies; see Stripe’s
        policies. Our emails do not contain tracking pixels and we do not track email opens or link clicks.
      </p>
      <p>If we add other non-essential cookies, we will update this Policy and, where the law requires, ask for your consent before using them.</p>

      <h2>3. How We Use Information</h2>
      <p>We use information to:</p>
      <ul>
        <li>Create, authenticate, and administer accounts.</li>
        <li>Provide website checks, response-time measurements, incident tracking, and notifications.</li>
        <li>Display monitoring history and enforce plan-specific limits.</li>
        <li>Create, manage, renew, and administer subscriptions, and handle billing questions through Stripe.</li>
        <li>Send account and security emails, such as email verification, password reset, and alerts.</li>
        <li>Respond to support requests.</li>
        <li>Maintain, troubleshoot, and improve the reliability and performance of the Service.</li>
        <li>Detect fraud, abuse, unauthorized access, and security incidents.</li>
        <li>Enforce our Terms and Conditions.</li>
        <li>Comply with legal obligations and respond to lawful requests, and establish, exercise, or defend legal claims.</li>
      </ul>
      <p>
        Where applicable law requires a legal basis for processing, we rely on: performance of a contract (providing the Service you signed up for); our legitimate interests in operating,
        securing, and improving the Service and preventing abuse, which are not overridden by your rights; compliance with legal obligations; and consent, where we ask for it. Our legitimate interest in understanding how the website is used is the basis for Google Analytics, and you can opt out at any time. We do not use your
        information for advertising and we do not make decisions about you by automated means that have legal or similarly significant effects.
      </p>

      <h2>4. How We Share Information</h2>
      <p>We do not sell your personal information. We share information only in the following circumstances.</p>

      <h3>A. Service providers</h3>
      <p>We use these providers to operate HawkEye. They may process information only to provide their services to us.</p>
      <ul>
        <li><strong>Vultr</strong>: server hosting. Our application and database run on servers in the United States. If we run checks from additional locations, the servers there also temporarily handle your monitor settings and credentials while they perform your checks.</li>
        <li><strong>Stripe</strong>: payment processing and subscription billing.</li>
        <li><strong>Resend</strong>: delivery of our emails, such as verification, password reset, and alert emails. Resend processes the recipient address and the content of those emails.</li>
        <li><strong>Cloudflare</strong>: domain name (DNS) services and the forwarding of messages sent to our support address.</li>
        <li><strong>Google (Gmail)</strong>: the mailbox where we read and answer support messages.</li>
        <li><strong>Google (Analytics)</strong>: website usage analytics, unless you opt out, as described in Section 2.C and E. Google receives page addresses without query strings or fragments, your IP address, and browser and device information.</li>
      </ul>
      <p>
        To find when a domain’s registration expires, we ask the public registry that runs the domain’s top-level domain (and the Internet Assigned Numbers Authority, which tells us which registry to ask). Those requests contain the domain name
        and our server’s address, but nothing about your account, and registries handle them under their own policies.
      </p>

      <h3>B. Payment processing</h3>
      <p>
        Information is shared with Stripe and relevant financial institutions and payment networks as necessary to complete transactions, manage subscriptions, prevent fraud, and meet legal requirements.
      </p>

      <h3>C. Legal, safety, and security reasons</h3>
      <p>
        We may disclose information when reasonably necessary to comply with applicable law, respond to lawful legal process, protect the rights or safety of HawkEye or others, investigate suspected fraud or abuse,
        or enforce our agreements.
      </p>

      <h3>D. Business transfers</h3>
      <p>If HawkEye is involved in a merger, acquisition, financing, reorganization, or sale of assets, information may be transferred as part of that transaction, subject to applicable legal requirements.</p>

      <h3>E. At your direction</h3>
      <p>We may disclose information when you request or authorize us to do so, for example by adding an alert recipient.</p>

      <h3>F. Slack and webhook destinations</h3>
      <p>
        If you add a Slack or webhook destination, we send alert content to the address you provide: the monitor’s name and web address, the time of the event, a short description of the failure (or the certificate
        or domain expiry date), and a link back to HawkEye. That destination is operated by you or by a third party you chose (for example Slack), and its handling of the information is governed by its own terms and policies, not this
        Policy. Treat these addresses as secrets: anyone who has a Slack webhook address can post to your channel.
      </p>

      <h3>G. Information on a public status page</h3>
      <p>Information you publish on a status page (see Section 2) is visible to anyone with its address.</p>

      <p>
        We do not sell personal information or share it for cross-context behavioral advertising, and we do not use it for targeted advertising.
      </p>

      <h2>5. Data Retention</h2>
      <p>Monitoring results and related history are kept for the period included in your subscription plan, then deleted automatically (our systems check hourly):</p>
      <ul>
        <li><strong>Free:</strong> 7 days.</li>
        <li><strong>Pro:</strong> 90 days.</li>
        <li><strong>Agency:</strong> 365 days.</li>
      </ul>
      <p>Other retention periods:</p>
      <ul>
        <li><strong>Resolved incidents</strong> follow the same plan periods. Incidents that are still open are kept until they are resolved.</li>
        <li><strong>Monitors you delete</strong> are permanently removed after 30 days. Their credentials, custom headers, and request body are erased immediately when you delete them.</li>
        <li><strong>Sessions and one-time links</strong> (sign-in sessions, password-reset and verification links) expire automatically, within 14 days, 1 hour, and 48 hours respectively, and are then removed.</li>
        <li><strong>Copies of Stripe event notifications</strong> are deleted after 90 days (180 days if processing failed).</li>
        <li><strong>Rate-limit counters</strong> are deleted within 24 hours.</li>
        <li><strong>Support messages</strong> are kept in our support mailbox for as long as needed to help you and to keep a record of the request.</li>
        <li><strong>Backups:</strong> we keep routine backups for disaster recovery for up to about 60 days. Data you delete from the live Service can remain in backups until they expire, and is not used for any other purpose. Stored monitor credentials remain encrypted in backups, and the encryption key is kept separately from them.</li>
        <li><strong>Billing records:</strong> Stripe keeps the transaction records it is legally required to keep. We keep only the identifiers and subscription state described in Section 2D, and delete them when you delete your account.</li>
      </ul>
      <p>
        <strong>Closing your account.</strong> You can delete your account at any time under <Link to="/account">Account</Link>. Deletion is immediate: your account, your workspaces and monitoring history, your alert destinations (including stored Slack and webhook addresses), maintenance windows and
        status page are removed, and your subscription is cancelled and your Stripe customer record deleted. You can download your data first using the export on the same page. If you own a workspace that has other members, you must remove them or
        transfer it before you can delete your account.
      </p>
      <p>
        <strong>Changing plans.</strong> If you move to a plan with shorter history, existing monitors are kept, and history older than the new plan’s period is removed on our next hourly clean-up. We do not hold it back.
      </p>
      <p>
        We may keep information longer where the law requires it or where it is reasonably necessary for security, fraud prevention, dispute resolution, or legal compliance.
      </p>

      <h2>6. Security</h2>
      <p>
        We use reasonable administrative, technical, and organizational measures designed to protect information against unauthorized access, loss, misuse, alteration, or disclosure. These include:
      </p>
      <ul>
        <li>Encrypted connections (HTTPS) for all traffic to and from the Service, with strict transport security.</li>
        <li>Passwords stored only as Argon2id hashes; one-time tokens stored only as hashes.</li>
        <li>Monitor credentials (passwords, tokens, and API keys you give us for API checks) encrypted before storage, and never returned by our API or included in data exports.</li>
        <li>Strict separation of each customer’s data, and protection against cross-site request forgery.</li>
        <li>Restrictions that stop monitoring checks from reaching private or internal networks.</li>
        <li>Rate limiting and abuse protection on sign-in and email-sending features.</li>
        <li>Application servers that run in containers with minimal privileges.</li>
      </ul>
      <p>
        No internet-based service or storage system can be guaranteed to be completely secure. We cannot promise absolute security, and you should protect your account credentials and avoid submitting secrets or
        sensitive information in monitor configurations. If a security incident affects your personal information, we will notify you and the relevant authorities as the law requires.
      </p>

      <h2>7. International Processing</h2>
      <p>
        Our servers are in the United States, and our service providers may process information in the United States and other countries where they operate. If you use the Service from outside the United States, your information
        will be transferred to, stored, and processed in the United States, where privacy laws may differ from those where you live.
      </p>
      <p>
        Where required by law, we rely on appropriate safeguards for these transfers, such as the European Commission’s Standard Contractual Clauses (and the UK equivalent) included in our providers’ data-processing terms, or
        a provider’s participation in the EU–U.S. Data Privacy Framework and its UK extension. Contact us if you would like more information.
      </p>

      <h2>8. Your Privacy Rights</h2>
      <p>Depending on where you live and the laws that apply, you may have the right to:</p>
      <ul>
        <li>Access the personal information we hold about you.</li>
        <li>Correct inaccurate information.</li>
        <li>Delete certain information.</li>
        <li>Receive a copy of certain information in a portable format.</li>
        <li>Object to or restrict certain processing.</li>
        <li>Withdraw consent where processing is based on consent.</li>
        <li>Opt out of sales, sharing for targeted advertising, or profiling (we do not engage in these).</li>
        <li>Appeal our decision about a privacy request, where the law provides that right.</li>
        <li>Lodge a complaint with a privacy or data-protection authority.</li>
      </ul>
      <p>
        You can exercise the rights to access, portability, and deletion yourself under <Link to="/account">Account</Link>. For any other request, contact <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> with the subject line
        <strong> “Privacy Request.”</strong> Please identify the account involved and describe your request. We may need to verify your identity or authority before acting.
      </p>
      <p>
        We will respond within the time required by applicable law (generally within 45 days, or one month where GDPR applies). We will not unlawfully discriminate against you for exercising a privacy right. These rights are
        subject to legal conditions and exceptions: for example, we may need to keep certain records for legal compliance, security, billing, or dispute resolution.
      </p>

      <h2>9. United States State Privacy Laws</h2>
      <p>
        Certain U.S. state privacy laws give residents specific rights over their personal information, including the rights listed in Section 8. Whether a particular state law applies to HawkEye depends on its
        scope and thresholds. Where an applicable law gives you additional rights, we will honor them.
      </p>
      <p>
        In the past 12 months we have collected the categories of information described in Section 2 (identifiers and contact details, account and subscription records, monitoring and usage data, and technical data such as IP addresses) for the
        purposes described in Section 3, and disclosed them to the service providers described in Section 4. We have not sold personal information or shared it for cross-context behavioral advertising.
      </p>
      <p>
        To exercise your rights, use the methods in Section 8. If we decline a request, you may appeal by replying to our response, or by emailing <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> with the subject line
        <strong> “Privacy Appeal.”</strong> We will respond to appeals within the time the applicable law requires. Because we do not engage in targeted advertising or sales of personal information, we do not need to honor opt-out preference signals for those activities.
      </p>

      <h2>10. European Economic Area, United Kingdom, and Other Regions</h2>
      <p>If you are in the European Economic Area (EEA), the United Kingdom, or another region with comprehensive data-protection law, the following also applies.</p>
      <ul>
        <li><strong>Controller.</strong> Hawkeye is the controller of the personal information described in this Policy. For the website-monitoring data you ask us to process on your behalf, we act on your instructions as your service provider (processor); our Terms and this Policy describe that processing. Contact us if you need a data-processing agreement.</li>
        <li><strong>Legal bases.</strong> We process personal information on the bases described in Section 3: contract, legitimate interests, legal obligation, and consent where we ask for it.</li>
        <li><strong>Your rights.</strong> You have the rights described in Section 8, including access, rectification, erasure, restriction, objection, data portability, and the right to withdraw consent at any time. You may also complain to your local supervisory authority.</li>
        <li><strong>Transfers.</strong> Your information is processed in the United States, using the safeguards described in Section 7.</li>
      </ul>

      <h2>11. Children</h2>
      <p>
        HawkEye is intended for business and general website-monitoring use, not for children. You must be at least 18 years old to create an account or use the Service.
      </p>
      <p>
        We do not knowingly collect personal information from anyone under 18. If you believe a child has provided personal information to HawkEye, contact <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> so we can review the matter and delete it.
      </p>

      <h2>12. Third-Party Websites and Services</h2>
      <p>The Service may link to third-party websites or integrate with third-party services. Their privacy practices are governed by their own policies, not this Policy. Review their policies before providing information to them.</p>

      <h2>13. Changes to This Policy</h2>
      <p>
        We may update this Policy to reflect changes in the Service, our practices, or legal requirements. We will update the “Last updated” date when changes are made. For material changes, we will notify you by email or through the
        Service before they take effect, and where the law requires it we will obtain your consent. Continued use of the Service does not override any consent or notice requirements imposed by applicable law.
      </p>

      <h2>14. Contact Us</h2>
      <p>For privacy questions or requests, contact:</p>
      <p>
        <strong>Hawkeye</strong><br />
        Email: <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a><br />
        Contact form: <Link to="/contact">hawkeye.observer/contact</Link>
      </p>
    </Shell>
  )
}

export function TermsPage() {
  return (
    <Shell title="HawkEye Terms and Conditions" draft={false}>
      <p><strong>Effective date:</strong> October 14, 2026<br /><strong>Last updated:</strong> October 15, 2026</p>
      <p>
        These Terms and Conditions (“Terms”) govern access to and use of the HawkEye website, applications, and website-monitoring services (together, the “Service”).
      </p>
      <p>The Service is operated by <strong>Hawkeye</strong> (“HawkEye,” “we,” “us,” or “our”).</p>
      <p>
        By creating an account, purchasing a subscription, or using the Service, you agree to these Terms. If you do not agree, do not use the Service.
      </p>

      <h2>1. Business Information and Contact</h2>
      <ul>
        <li><strong>Legal business name:</strong> Hawkeye, a limited liability company</li>
        <li><strong>Email:</strong> <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> (or use our <Link to="/contact">contact form</Link>)</li>
        <li><strong>Website:</strong> https://hawkeye.observer</li>
      </ul>

      <h2>2. Eligibility and Accounts</h2>
      <p>
        You must be at least 18 years old and legally able to enter into a binding agreement under the laws that apply to you in order to use the Service.
      </p>
      <p>
        If you use the Service on behalf of a company, agency, or other organization, you represent that you have authority to bind that organization to these Terms. In that case, “you” includes the organization.
      </p>
      <p>
        You agree to provide accurate account information, maintain the security of your login credentials, and promptly update information that becomes inaccurate.
      </p>
      <p>
        You are responsible for activities conducted through your account, except to the extent caused by circumstances for which applicable law makes HawkEye responsible. Notify us promptly at{' '}
        <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> if you suspect unauthorized access.
      </p>

      <h2>3. Description of the Service</h2>
      <p>
        HawkEye is a website-monitoring service designed to help users monitor website availability, record response times, track incidents, and receive configured notifications.
      </p>
      <p>Depending on your plan, the Service includes:</p>
      <ul>
        <li>Periodic HTTP or HTTPS availability checks, including API checks with a chosen request method, custom headers, a request body, authentication, and checks of the response.</li>
        <li>Response-time measurements and monitoring history.</li>
        <li>On paid plans, checks of a monitor from more than one location, where the Service offers them, with the monitor treated as down only when most of its locations report a failure.</li>
        <li>SSL certificate expiry tracking for HTTPS monitors, with email warnings as the expiry date approaches (currently 30, 14, 7, and 1 days before, and when the certificate has expired).</li>
        <li>Domain expiry tracking, with email warnings as a domain’s registration expiry date approaches (currently 30, 14, 7, and 1 days before, and when it has expired).</li>
        <li>Incident detection, incident records, and recovery status.</li>
        <li>Alert notifications by email, to a Slack channel, or to a webhook address you provide, as described in Section 9.</li>
        <li>An optional public status page showing the monitors you choose.</li>
        <li>Maintenance windows, on the plans that include them, during which alerts and incidents are suppressed for the monitors you choose.</li>
        <li>Dashboard views and historical uptime and response-time reports.</li>
        <li>Plan-based monitor limits, check intervals, and history-retention periods.</li>
      </ul>
      <p>
        Features, limits, and availability may differ by plan and may change as the Service develops. The current plan descriptions and checkout information form part of the offer presented to you.
        Marketing material is not a guarantee that a feature is available in every plan or in every technical circumstance. If a feature is expressly included in your paid plan, any material change to that
        entitlement will be handled subject to applicable law and the terms of your subscription.
      </p>

      <h2>4. Your Authority to Monitor Websites</h2>
      <p>
        You may configure HawkEye to monitor only websites, domains, and endpoints that you own or are authorized to test or monitor. You are responsible for ensuring that your use of the Service complies with
        applicable laws, contracts, website terms, and third-party rights.
      </p>
      <p>You must not use HawkEye to:</p>
      <ul>
        <li>Monitor a website or endpoint without the necessary authorization.</li>
        <li>Send alerts to a Slack channel or webhook address that you are not authorized to use, or use alerts to send unsolicited or unlawful content to others.</li>
        <li>Publish on a status page anything that is unlawful, infringing, misleading, or that you are not authorized to publish, including another party’s name or marks in a way that suggests their endorsement.</li>
        <li>Conduct denial-of-service attacks, stress tests, or excessive request campaigns.</li>
        <li>Attempt to bypass authentication, access controls, rate limits, or security measures.</li>
        <li>Probe internal networks, private systems, or infrastructure without explicit authorization. Checks are restricted from reaching private networks, and attempts to get around that restriction are prohibited.</li>
        <li>Interfere with or impair the operation of any website, network, or service.</li>
        <li>Place credentials, confidential data, or sensitive personal information anywhere in a monitor except its Authentication fields. In particular, do not put secrets in the URL, custom headers, or request body: those are not encrypted and are visible to the other members of your organization.</li>
        <li>Send requests that create, change, or delete data on a system you are not authorized to modify.</li>
        <li>Harass, threaten, defraud, or unlawfully surveil others.</li>
        <li>Circumvent subscription limits, security controls, or access restrictions.</li>
        <li>Reverse engineer or exploit the Service, except where applicable law expressly permits it.</li>
        <li>Use the Service in a manner that violates applicable law or infringes another party’s rights.</li>
      </ul>
      <p>
        <strong>Requests that change data.</strong> Monitors that use the POST, PUT, PATCH, or DELETE methods send that request on every check. You are solely responsible for the effects of the requests
        your monitors send, including requests that create, change, or delete data, and for choosing endpoints that are safe to call repeatedly. To limit misuse, these monitors require a verified email address
        and run at most every five minutes. You authorize us to store the credentials you provide, encrypted, and to use them only to make the checks you configure.
      </p>
      <p>
        We may suspend or restrict monitoring that reasonably appears to violate these Terms, create a security risk, or expose HawkEye or others to material harm. Where reasonably practicable and legally
        permitted, we will provide notice and an opportunity to address the issue.
      </p>

      <h2>5. Free and Paid Plans</h2>
      <p>
        HawkEye offers a free plan and paid subscription plans. Available features, monitor limits, check intervals, and retention periods are described on the pricing page and at checkout. At the time of these Terms:
      </p>
      <ul>
        <li><strong>Free:</strong> up to 5 monitors, checks every 5 minutes, and 7 days of monitoring history.</li>
        <li><strong>Pro:</strong> up to 50 monitors, checks every minute, and 90 days of monitoring history.</li>
        <li><strong>Agency:</strong> up to 200 monitors, checks every minute, and 365 days of monitoring history.</li>
      </ul>
      <p>
        Paid plans may check each monitor from several locations, up to the limit shown at checkout. Every plan includes email, Slack, and webhook alerts and one public status page. Maintenance windows are included in the Pro and Agency plans. If you move to a plan that does not include maintenance windows, windows you
        already created keep working, but you cannot create new ones.
      </p>
      <p>
        We enforce plan limits through the application. If your account exceeds the limits of a plan (for example, after a downgrade), your existing monitors are kept, but we may prevent you from adding monitors or
        using features reserved for a higher plan until you are within the limits, consistently with the plan terms and applicable law.
      </p>

      <h2>6. Subscription Billing Through Stripe</h2>
      <p>
        Paid subscriptions are processed using Stripe. By purchasing a subscription, you authorize the applicable charges through the payment method you select, subject to the terms presented at checkout.
      </p>

      <h3>A. Prices and billing periods</h3>
      <p>
        The applicable price, billing interval, currency, taxes, and subscription terms are displayed before you complete your purchase. Current prices, in US dollars, are $19 per month for Pro and $59 per month for
        Agency. Subscriptions are currently billed monthly. We do not currently offer annual plans or free trials of paid plans.
      </p>

      <h3>B. Recurring payments</h3>
      <p>
        Unless the checkout offer states otherwise, a paid subscription renews automatically for the disclosed billing period until canceled. Before you authorize payment, HawkEye will provide the applicable price, billing
        frequency, renewal terms, and cancellation method as required by law. You authorize recurring charges until your subscription is canceled or otherwise ends. We will not rely on this provision to bypass any consent,
        disclosure, notification, or cancellation requirement imposed by applicable law.
      </p>

      <h3>C. Stripe and payment information</h3>
      <p>
        Stripe processes payments and supports subscription management. Your payment information is also subject to the applicable terms and privacy disclosures of Stripe and any relevant financial institution or payment
        provider. HawkEye receives billing status, subscription identifiers, and related information needed to administer your account, as described in our <Link to="/privacy">Privacy Policy</Link>. Your card details are
        entered on Stripe’s pages and are not stored by HawkEye.
      </p>

      <h3>D. Failed payments</h3>
      <p>
        If a payment fails, Stripe or HawkEye may notify you and Stripe may retry the charge. While a failed payment is being retried, your paid features remain available. If the payment remains unresolved and the subscription is
        ended, your account returns to the Free plan: your existing monitors and data are kept, subject to the Free plan’s limits and retention period. This is subject to applicable law and any required notice or grace period.
      </p>

      <h3>E. Taxes</h3>
      <p>
        Prices are presented with any applicable taxes or additional charges as required by law. You are responsible for taxes applicable to your purchase, except for taxes imposed on HawkEye’s income.
      </p>

      <h3>F. Price changes</h3>
      <p>
        We may change subscription prices prospectively. We will provide advance notice and obtain any consent required by applicable law before charging a new price. If you do not accept a price change, you may cancel
        before the change takes effect, subject to your rights under applicable law.
      </p>

      <h2>7. Cancellation, Renewal, and Refunds</h2>

      <h3>A. Cancellation</h3>
      <p>
        You may cancel your subscription at any time from the <Link to="/billing">Billing</Link> page in your account, or by contacting <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a> if that is unavailable.
        Unless the checkout terms or applicable law provide otherwise, cancellation stops future renewals and paid access continues through the end of the current paid billing period.
      </p>

      <h3>B. Automatic renewal notices</h3>
      <p>
        HawkEye will provide the disclosures, acknowledgments, renewal reminders, and other notices required by applicable law, within the applicable time frame and through a permitted communication method.
      </p>

      <h3>C. Refunds</h3>
      <p>
        Unless required by law or expressly stated in the offer, payments are not refundable for partially used billing periods after a subscription has begun. This policy does not limit any non-waivable statutory rights,
        mandatory refund entitlements, or rights arising from a billing error or unauthorized transaction. If you believe you were charged in error, contact us at <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a>.
      </p>

      <h3>D. Cancellation confirmation</h3>
      <p>Where required, HawkEye will provide confirmation of cancellation and disclose the date on which renewal stops and any remaining access ends.</p>

      <h2>8. Monitoring Accuracy and Service Limitations</h2>
      <p>
        HawkEye performs periodic checks. Monitoring results are observations made from the Service’s monitoring environment at particular times; they are not a guarantee of how a website will behave for every visitor,
        network, or location.
      </p>
      <p>
        Monitoring may be affected by network conditions, DNS resolution, hosting-provider issues, firewalls, rate limits, third-party outages, configuration errors, or other circumstances outside HawkEye’s control.
      </p>
      <p>
        Certificate expiry information is read from the certificate the monitored address presented to HawkEye during a check. It can differ from what other visitors see (for example, if your site serves different certificates to different locations or hostnames), and HawkEye does not detect revoked or otherwise invalid certificates other than by failing the check. Certificate warnings are a courtesy reminder; you remain responsible for renewing your certificates.
      </p>
      <p>
        Domain expiry dates are read from public domain registries, which differ in what they publish: some do not share expiry dates, some hide them for privacy, and a date can be out of date or wrong. Where HawkEye cannot find a date it says so,
        and that is not a statement that the domain is safe. Domain warnings are a courtesy reminder; you remain responsible for renewing your domains with your registrar.
      </p>
      <p>
        Response checks evaluate the response body (up to its first 1 MB) at the time of the check. A successful check does not prove that a website is free from defects or security vulnerabilities. A failed check does not necessarily establish that a website is unavailable to all visitors.
      </p>
      <p>
        HawkEye cannot guarantee that every outage or performance problem will be detected, that every alert will be delivered immediately, or that all incidents will be correctly classified. You remain responsible for
        investigating monitoring results and taking appropriate action.
      </p>
      <p>
        Unless a separate written agreement expressly provides otherwise, the Service is provided on a best-effort basis, has no service-level commitment, and is not intended to be an emergency-response system, a
        safety-critical monitoring system, or a substitute for your own operational and security controls.
      </p>

      <h2>9. Alerts, Maintenance Windows, and Status Pages</h2>

      <h3>A. Alerts and notifications</h3>
      <p>
        You may configure the Service to send notifications about incidents, recovery events, and approaching certificate and domain expiry to email addresses, to a Slack channel through a Slack incoming-webhook address, and to a webhook
        address of your choice. SMS is not currently available. You are responsible for providing valid destinations, confirming email addresses as the Service requires,
        keeping your notification settings current, and for the systems that receive webhooks, including verifying the signature we send and handling repeated deliveries.
      </p>
      <p>
        A Slack or webhook address is a credential. Keep it confidential: anyone who has it may be able to post to your Slack channel or to send requests to your endpoint pretending to be HawkEye. We store these addresses
        encrypted, but you are responsible for revoking and replacing any that you believe have been exposed. Slack and your own endpoints are third-party systems outside HawkEye’s control, and their terms apply to your use of them.
      </p>
      <p>
        Alerts may be delayed, filtered, rejected, rate-limited, duplicated, or interrupted by systems outside HawkEye’s control, including email providers, Slack, and your own servers. We retry failed deliveries for a limited
        time and then stop. You should not rely on any single alert as your sole means of detecting or responding to a critical event.
      </p>

      <h3>B. Maintenance windows</h3>
      <p>
        During a maintenance window you schedule, the Service continues to run checks and record them, but does not open incidents, send alerts, or change a monitor’s status for the monitors the window covers, and excludes
        those checks from uptime figures. You are responsible for the times, repeat settings, and monitors you select. We do not alert you to problems that occur during a window. If a monitor is still unavailable when a window ends,
        alerting resumes only after the Service’s normal confirmation checks. Repeating windows run at fixed 24-hour or 7-day intervals in UTC and do not adjust for daylight saving time.
      </p>

      <h3>C. Public status pages</h3>
      <p>
        If you publish a status page, the information described in the Privacy Policy becomes visible to anyone with its address. You are solely responsible for what you publish and for choosing the monitors and names shown.
        Status pages are generated automatically from check results, which are periodic and may be incomplete, delayed, or wrong. A status page is not a guarantee, certification, or statement by HawkEye about the availability of
        your services, and you should not present it as one. We may unpublish or remove a status page that violates these Terms.
      </p>

      <h2>10. Data, Monitoring History, and Retention</h2>
      <p>
        You retain any rights you have in information and content that you submit to the Service. You grant HawkEye permission to process that information as reasonably necessary to provide, secure, and maintain the Service,
        administer your account, and comply with applicable law.
      </p>
      <p>HawkEye stores monitoring results, response-time measurements, incident records, and related information for the period included in your subscription plan:</p>
      <ul>
        <li>Free: 7 days.</li>
        <li>Pro: 90 days.</li>
        <li>Agency: 365 days.</li>
      </ul>
      <p>
        Older history is deleted automatically. If you move to a plan with a shorter period, history older than the new period is removed. If your subscription ends, your account returns to the Free plan and Free plan
        retention applies. You can export your data and delete your account at any time from your account page; deletion is immediate and described in the <Link to="/privacy">Privacy Policy</Link>.
      </p>
      <p>
        HawkEye may retain billing, security, support, and other records for different periods where reasonably necessary for legitimate business purposes or required by law. Your use of the Service is also subject to
        the HawkEye <Link to="/privacy">Privacy Policy</Link>, which describes how personal information is handled.
      </p>

      <h2>11. Intellectual Property</h2>
      <p>
        The Service, including its software, design, branding, text, graphics, and other materials provided by HawkEye, is owned by HawkEye or its licensors and is protected by applicable intellectual-property laws.
      </p>
      <p>
        Subject to these Terms and payment of applicable subscription fees, HawkEye grants you a limited, non-exclusive, non-transferable, revocable license to access and use the Service for its intended purpose during your
        permitted use of the Service.
      </p>
      <p>
        You may not copy, distribute, resell, sublicense, or create derivative works from the Service except as expressly authorized by HawkEye or permitted by applicable law. You may not use HawkEye’s name, logos, or
        other branding in a way that implies endorsement or affiliation without prior written permission.
      </p>

      <h2>12. Feedback</h2>
      <p>
        If you provide suggestions, ideas, or feedback about the Service, you grant HawkEye permission to use that feedback to improve or develop the Service without compensation or restriction, except where prohibited by
        law. This provision does not transfer ownership of your pre-existing intellectual property.
      </p>

      <h2>13. Third-Party Services</h2>
      <p>
        The Service relies on or links to third-party services, including Stripe and infrastructure and email providers. HawkEye does not control all third-party systems and is not responsible for their independent terms,
        policies, or availability, except to the extent applicable law provides otherwise. Your use of third-party services may be subject to separate agreements between you and the relevant provider.
      </p>

      <h2>14. Suspension and Termination</h2>
      <p>You may stop using the Service at any time and cancel paid subscriptions in accordance with Section 7.</p>
      <p>
        HawkEye may suspend or terminate access if reasonably necessary to address a material breach of these Terms, nonpayment, fraud, abuse, security threats, unlawful activity, or a risk to the Service or its users.
        Where reasonably practicable and legally permitted, we will provide notice and a reasonable opportunity to resolve the issue before taking action. We may act immediately when necessary to address urgent security
        risks, unlawful conduct, or material harm.
      </p>
      <p>
        We may discontinue or materially change the Service, subject to applicable law and any contractual commitments that apply to your subscription. If we discontinue a paid feature or the Service in a manner that
        materially affects a paid subscription, we will address any applicable notice, refund, or other obligations required by law or the relevant offer.
      </p>
      <p>
        Upon termination, your access to the Service may end. Data handling and retention are governed by the Privacy Policy, the applicable plan terms, and any mandatory legal requirements.
      </p>

      <h2>15. Disclaimer of Warranties</h2>
      <p>
        To the maximum extent permitted by applicable law, the Service is provided on an “as is” and “as available” basis. HawkEye disclaims warranties that the law permits it to disclaim, including implied warranties of
        merchantability, fitness for a particular purpose, and non-infringement.
      </p>
      <p>
        HawkEye does not warrant that the Service will be uninterrupted, error-free, completely secure, or capable of detecting every incident. Nothing in these Terms excludes a warranty or consumer right that cannot
        lawfully be excluded.
      </p>

      <h2>16. Limitation of Liability</h2>
      <p>
        To the maximum extent permitted by applicable law, HawkEye will not be liable for indirect, incidental, special, consequential, exemplary, or punitive damages, or for loss of profits, revenue, business
        opportunities, goodwill, data, or business interruption arising from or related to your use of or inability to use the Service.
      </p>
      <p>To the maximum extent permitted by applicable law, HawkEye’s aggregate liability for claims arising out of or relating to the Service or these Terms will not exceed the greater of:</p>
      <ol>
        <li>The total subscription fees you paid to HawkEye for the Service during the twelve months immediately preceding the event giving rise to the claim; or</li>
        <li>US $100.</li>
      </ol>
      <p>This limitation applies to the extent permitted by law and does not limit liability that cannot lawfully be limited or excluded. It does not affect any non-waivable consumer rights.</p>

      <h2>17. Indemnification</h2>
      <p>
        To the extent permitted by applicable law, you agree to be responsible for third-party claims, losses, and reasonable costs arising from your unlawful use of the Service, your unauthorized monitoring of
        third-party systems, or your material breach of these Terms.
      </p>
      <p>
        This provision does not require you to indemnify HawkEye for losses caused by HawkEye’s own conduct where such indemnification would be unlawful or unenforceable.
      </p>

      <h2>18. Governing Law and Disputes</h2>
      <p>
        These Terms are governed by the laws of the <strong>Commonwealth of Pennsylvania, United States</strong>, without regard to conflict-of-law principles, except where mandatory law requires otherwise.
      </p>
      <p>
        Subject to any non-waivable rights or mandatory jurisdictional rules, disputes arising from these Terms or the Service will be brought in the state or federal courts located in the Commonwealth of Pennsylvania, and
        the parties consent to the jurisdiction of those courts.
      </p>
      <p>Nothing in this section prevents a consumer from bringing a claim in a forum where applicable law gives that consumer a non-waivable right to do so.</p>

      <h2>19. Changes to These Terms</h2>
      <p>
        We may update these Terms to reflect changes in the Service, our business, or applicable law. We will update the “Last updated” date when changes are made. Where a change is material or applicable law requires it,
        we will provide appropriate notice, for example by email or through the Service, before it takes effect, and obtain any required consent. Continued use of the Service will not substitute for consent where consent is
        legally required.
      </p>

      <h2>20. General Provisions</h2>
      <p>If any provision of these Terms is found unenforceable, the remaining provisions will remain in effect to the extent permitted by law.</p>
      <p>Our failure to enforce a provision does not waive our right to enforce it later.</p>
      <p>
        You may not transfer your rights or obligations under these Terms without our prior written consent, except where applicable law provides otherwise. HawkEye may transfer its rights and obligations as part of a
        merger, reorganization, or transfer of the relevant business, subject to applicable law.
      </p>
      <p>
        These Terms, together with the applicable plan details, checkout terms, and <Link to="/privacy">Privacy Policy</Link>, form the agreement governing your use of the Service. If a separate written agreement signed by
        HawkEye expressly conflicts with these Terms, that written agreement will control to the extent of the conflict.
      </p>
      <p>Nothing in these Terms creates a partnership, agency, employment, or joint-venture relationship between you and HawkEye.</p>

      <h2>21. Contact</h2>
      <p>Questions about these Terms, subscriptions, or the Service can be sent to:</p>
      <p>
        <strong>Hawkeye</strong><br />
        Email: <a href={`mailto:${SUPPORT}`}>{SUPPORT}</a><br />
        Contact form: <Link to="/contact">hawkeye.observer/contact</Link>
      </p>
    </Shell>
  )
}
