// What each page says about itself to browsers and search engines. This is the single source for three things:
//   * the title, description and robots tags the app sets when you navigate (usePageMeta),
//   * the per-page HTML written at build time (build/prerender-plugin.ts), so crawlers and link previews see them without running JavaScript,
//   * the generated sitemap.xml (every page with index: true).
// A path with a ":" is a pattern (one page, many addresses): it is titled in the browser but not prerendered or listed in the sitemap.
// KEEP IN STEP WITH THE ROUTES IN src/App.tsx (src/seo.test.ts checks this). This file has no imports: the build reads it directly.

export const SITE_URL = 'https://hawkeye.observer'
export const SITE_NAME = 'HawkEye'

export interface PageMeta {
  path: string
  /** Under about 60 characters, or search results cut it off. */
  title: string
  /** Under about 160 characters. */
  description: string
  /** true: listed in the sitemap and open to search engines. false: the page asks not to be indexed. */
  index: boolean
  /** Link-preview image (1200x630, made by scripts/prepare_share_images.py): a path under /share. Defaults to DEFAULT_SHARE_IMAGE. */
  image?: string
  /** Structured data (schema.org) written into the prerendered page. Only state facts the page itself shows. */
  jsonLd?: Record<string, unknown>
}

/** The preview image for a page that has none of its own. */
export const DEFAULT_SHARE_IMAGE = '/share/home.jpg'
export const SHARE_IMAGE_ALT = 'HawkEye website monitoring dashboard on a glowing globe'

export const HOME_TITLE = 'HawkEye — Website Uptime Monitoring & Alerts'
export const HOME_DESCRIPTION =
  'Website and API uptime monitoring with outage alerts by email, Slack or webhook, SSL and domain expiry warnings, and public status pages. Start free.'

/** A free tool page. With a name it also carries WebApplication structured data (a free, browser-based tool). */
function tool(path: string, title: string, description: string, name?: string): PageMeta {
  const page: PageMeta = { path, title, description, index: true, image: `/share/${path === '/tools' ? 'tools' : path.split('/').pop()}.jpg` }
  if (name) {
    page.jsonLd = {
      '@context': 'https://schema.org',
      '@type': 'WebApplication',
      name,
      url: `${SITE_URL}${path}`,
      applicationCategory: 'BusinessApplication',
      operatingSystem: 'Any',
      offers: { '@type': 'Offer', price: '0', priceCurrency: 'USD' },
      publisher: { '@type': 'Organization', name: SITE_NAME, url: SITE_URL },
    }
  }
  return page
}

export const PAGES: readonly PageMeta[] = [
  {
    path: '/',
    title: HOME_TITLE,
    description: HOME_DESCRIPTION,
    index: true,
    jsonLd: {
      '@context': 'https://schema.org',
      '@graph': [
        { '@type': 'Organization', name: SITE_NAME, url: SITE_URL, logo: `${SITE_URL}/icon-192.png` },
        { '@type': 'WebSite', name: SITE_NAME, url: SITE_URL },
      ],
    },
  },
  tool('/tools', 'Free Website Monitoring Tools | HawkEye', 'Free tools to check if a website is down, check an SSL certificate, plan uptime and estimate what downtime costs. No sign-up needed.'),
  tool('/tools/is-website-down', 'Is My Website Down? Free Status Checker | HawkEye', 'Check whether a website is down for everyone or just you. Enter a domain to see if it answers, its HTTP status code and response time. Free.', 'Website Status Checker'),
  tool('/tools/ssl-certificate-checker', 'Free SSL Certificate Checker | HawkEye', 'Check when an SSL certificate expires, who issued it and whether browsers trust it. Enter a domain for an instant result. Free, no sign-up.', 'SSL Certificate Checker'),
  tool('/tools/uptime-sla-calculator', 'Uptime SLA Calculator: Allowed Downtime | HawkEye', 'Turn an uptime percentage like 99.9% into allowed downtime per day, week, month, quarter and year. Or work out the uptime you delivered.', 'Uptime SLA Calculator'),
  tool('/tools/downtime-cost-calculator', 'Downtime Cost Calculator | HawkEye', 'Estimate the revenue a website outage puts at risk from your daily revenue, trading hours and how long the site is down. Free.', 'Downtime Cost Calculator'),
  { path: '/contact', title: 'Contact HawkEye', description: 'Questions about HawkEye website and API monitoring? Send us a message and we will get back to you.', index: true },
  { path: '/privacy', title: 'Privacy Policy | HawkEye', description: 'What personal information HawkEye collects, how it is used and shared, how long it is kept, and your privacy rights.', index: true },
  { path: '/terms', title: 'Terms of Service | HawkEye', description: 'The terms that apply when you use HawkEye website and API monitoring, including plans, acceptable use and your authority to monitor sites.', index: true },

  // Public, but not meant for search results.
  { path: '/monitoring', title: 'HawkEye: Know Before Your Customers Do', description: 'Know when your website goes down before your customers do. Start monitoring free with HawkEye.', index: false },
  { path: '/login', title: 'Log in | HawkEye', description: 'Log in to your HawkEye account.', index: false },
  { path: '/register', title: 'Create your free account | HawkEye', description: 'Create a free HawkEye account and start monitoring your websites and APIs.', index: false },
  { path: '/forgot-password', title: 'Reset your password | HawkEye', description: 'Request a link to reset your HawkEye password.', index: false },
  { path: '/reset-password', title: 'Choose a new password | HawkEye', description: 'Choose a new password for your HawkEye account.', index: false },
  { path: '/confirm-alert-address', title: 'Confirm alert address | HawkEye', description: 'Confirm an email address for HawkEye alerts.', index: false },
  { path: '/unsubscribe', title: 'Stop these emails | HawkEye', description: 'Stop getting-started emails from HawkEye.', index: false },

  // Signed-in app.
  { path: '/verify-email', title: 'Verify your email | HawkEye', description: 'Verify your email address for your HawkEye account.', index: false },
  { path: '/dashboard', title: 'Dashboard | HawkEye', description: 'Your HawkEye dashboard.', index: false },
  { path: '/monitors', title: 'Monitors | HawkEye', description: 'Your HawkEye monitors.', index: false },
  { path: '/monitors/new', title: 'Add a monitor | HawkEye', description: 'Add a website or API monitor.', index: false },
  { path: '/monitors/:monitorID', title: 'Monitor | HawkEye', description: 'Monitor details.', index: false },
  { path: '/monitors/:monitorID/edit', title: 'Edit monitor | HawkEye', description: 'Edit a monitor.', index: false },
  { path: '/incidents', title: 'Incidents | HawkEye', description: 'Your HawkEye incidents.', index: false },
  { path: '/notifications', title: 'Notifications | HawkEye', description: 'Your HawkEye alert channels.', index: false },
  { path: '/domains', title: 'Domains | HawkEye', description: 'Your HawkEye domain expiry monitoring.', index: false },
  { path: '/maintenance', title: 'Maintenance windows | HawkEye', description: 'Your HawkEye maintenance windows.', index: false },
  { path: '/reports', title: 'Reports | HawkEye', description: 'Your HawkEye monthly PDF reports.', index: false },
  { path: '/status-page', title: 'Status page | HawkEye', description: 'Your HawkEye public status page settings.', index: false },
  { path: '/billing', title: 'Billing | HawkEye', description: 'Your HawkEye plan and billing.', index: false },
  { path: '/account', title: 'Account | HawkEye', description: 'Your HawkEye account.', index: false },
]

/** Pages with one fixed address: the ones that can be written out at build time. */
export const STATIC_PAGES: readonly PageMeta[] = PAGES.filter((p) => !p.path.includes(':'))

/** The full public address of a path, which is what a canonical link and the sitemap use. */
export function canonicalUrl(path: string): string {
  return path === '/' ? `${SITE_URL}/` : `${SITE_URL}${path}`
}
