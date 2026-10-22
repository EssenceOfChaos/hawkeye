import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider } from '../../auth'
import { trackEvent } from '../../analytics'
import { DowntimeCostCalculatorPage } from './DowntimeCostCalculatorPage'
import { SslCheckerPage } from './SslCheckerPage'
import { ToolsIndexPage } from './ToolsIndexPage'
import { UptimeSlaCalculatorPage } from './UptimeSlaCalculatorPage'
import { WebsiteStatusPage } from './WebsiteStatusPage'

vi.mock('../../analytics', () => ({ trackEvent: vi.fn(), trackPageView: vi.fn(), openCookieSettings: vi.fn(), initAnalytics: vi.fn(), analyticsEnabled: () => false, useConsent: () => ({}) }))

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'x' } }), { status: 401, headers: { 'Content-Type': 'application/json' } }))))
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

function renderPage(page: React.ReactNode) {
  return render(
    <MemoryRouter>
      <AuthProvider>{page}</AuthProvider>
    </MemoryRouter>,
  )
}

describe('ToolsIndexPage', () => {
  it('links to each tool', () => {
    renderPage(<ToolsIndexPage />)
    expect(screen.getByRole('link', { name: 'SSL Certificate Checker' })).toHaveAttribute('href', '/tools/ssl-certificate-checker')
    expect(screen.getByRole('link', { name: 'Is My Website Down?' })).toHaveAttribute('href', '/tools/is-website-down')
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Free website monitoring tools')
    expect(screen.getByRole('link', { name: 'Uptime SLA Calculator' })).toHaveAttribute('href', '/tools/uptime-sla-calculator')
    expect(screen.getByRole('link', { name: 'Downtime Cost Calculator' })).toHaveAttribute('href', '/tools/downtime-cost-calculator')
  })
})

describe('UptimeSlaCalculatorPage', () => {
  it('shows what 99.9% allows, with no input needed', () => {
    renderPage(<UptimeSlaCalculatorPage />)
    expect(screen.getByRole('heading', { level: 1, name: 'Uptime SLA Calculator' })).toBeInTheDocument()
    const month = screen.getByText('Per month', { selector: 'dt' }).parentElement!
    expect(within(month).getByText('43 m 48 s')).toBeInTheDocument()
    expect(within(screen.getByText('Per year', { selector: 'dt' }).parentElement!).getByText('8 h 45 m 36 s')).toBeInTheDocument()
  })

  it('updates for a new target, and refuses one that is not a percentage', async () => {
    const user = userEvent.setup()
    renderPage(<UptimeSlaCalculatorPage />)
    const target = screen.getByLabelText('Uptime target (%)')
    await user.clear(target)
    await user.type(target, '99.99')
    expect(within(screen.getByText('Per year', { selector: 'dt' }).parentElement!).getByText('52 m 34 s')).toBeInTheDocument()
    await user.clear(target)
    await user.type(target, '120')
    expect(screen.getByText('Enter a percentage from 0 to 100.')).toBeInTheDocument()
    expect(screen.queryByText('Per year', { selector: 'dt' })).not.toBeInTheDocument()
  })

  it('lists the common targets in a table', () => {
    renderPage(<UptimeSlaCalculatorPage />)
    const table = screen.getByRole('table')
    expect(within(table).getByRole('rowheader', { name: /99\.999%/ })).toBeInTheDocument()
    expect(within(table).getAllByRole('row')).toHaveLength(7) // header + six targets
  })

  it('works out the uptime for an outage and which targets it met, without rounding a shortfall up', async () => {
    const user = userEvent.setup()
    renderPage(<UptimeSlaCalculatorPage />)
    expect(screen.getByText('99.8972% uptime')).toBeInTheDocument() // 45 min of a 43 m 48 s month budget: just short of 99.9
    await user.clear(screen.getByLabelText('Downtime (minutes)'))
    await user.type(screen.getByLabelText('Downtime (minutes)'), '10')
    expect(screen.getByText('99.9771% uptime')).toBeInTheDocument()
    expect(screen.getByText('99.9% ✓')).toBeInTheDocument()
    expect(screen.getByText('99.99% ✗')).toBeInTheDocument()
  })

  it('says so when the downtime is longer than the period', async () => {
    const user = userEvent.setup()
    renderPage(<UptimeSlaCalculatorPage />)
    await user.selectOptions(screen.getByLabelText('Over the'), 'day')
    await user.clear(screen.getByLabelText('Downtime (minutes)'))
    await user.type(screen.getByLabelText('Downtime (minutes)'), '2000')
    expect(screen.getByText('That is longer than the period.')).toBeInTheDocument()
    expect(screen.queryByText(/% uptime$/)).not.toBeInTheDocument()
  })

  it('records one tool_use however many inputs change, and a click on the sign-up button', async () => {
    const user = userEvent.setup()
    renderPage(<UptimeSlaCalculatorPage />)
    await user.type(screen.getByLabelText('Uptime target (%)'), '9')
    await user.type(screen.getByLabelText('Uptime target (%)'), '9')
    expect(trackEvent).toHaveBeenCalledTimes(1)
    expect(trackEvent).toHaveBeenCalledWith('tool_use', { tool: 'uptime-sla-calculator' })
    const cta = screen.getByRole('link', { name: 'Start monitoring free' })
    expect(cta).toHaveAttribute('href', '/register')
    await user.click(cta)
    expect(trackEvent).toHaveBeenLastCalledWith('tool_cta_click', { tool: 'uptime-sla-calculator' })
  })
})

describe('DowntimeCostCalculatorPage', () => {
  it('estimates from the example values: $5,000 a day, 24 hours, 1.5 hour outage = $313', () => {
    renderPage(<DowntimeCostCalculatorPage />)
    expect(screen.getByRole('heading', { level: 1, name: 'Downtime Cost Calculator' })).toBeInTheDocument()
    expect(screen.getAllByText('$313')).toHaveLength(2) // 5000 / 24 × 1.5 = 312.5, shown as the total and as revenue at risk
    expect(screen.getByText('$625')).toBeInTheDocument() // two such outages a year: 2 × 312.50
  })

  it('recalculates when an input changes', async () => {
    const user = userEvent.setup()
    renderPage(<DowntimeCostCalculatorPage />)
    await user.clear(screen.getByLabelText('Average revenue per day ($)'))
    await user.type(screen.getByLabelText('Average revenue per day ($)'), '2400')
    await user.clear(screen.getByLabelText('Hours'))
    await user.type(screen.getByLabelText('Hours'), '2')
    await user.clear(screen.getByLabelText('Minutes'))
    await user.type(screen.getByLabelText('Minutes'), '0')
    expect(screen.getAllByText('$200').length).toBeGreaterThan(0)
  })

  it('explains an input it cannot use instead of showing a result', async () => {
    const user = userEvent.setup()
    renderPage(<DowntimeCostCalculatorPage />)
    await user.clear(screen.getByLabelText('Hours per day you take orders'))
    await user.type(screen.getByLabelText('Hours per day you take orders'), '0')
    expect(screen.getByText('Enter hours from 1 to 24.')).toBeInTheDocument()
    expect(screen.getByText('Fix the highlighted fields to see the estimate.')).toBeInTheDocument()
  })

  it('sends a signed-in visitor to the dashboard instead of sign-up', async () => {
    const user = { id: 'u', email: 'a@example.com', email_verified_at: '2026-01-01T00:00:00Z' }
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ user, organizations: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))))
    renderPage(<DowntimeCostCalculatorPage />)
    expect(await screen.findByRole('link', { name: 'Open dashboard' })).toHaveAttribute('href', '/dashboard')
  })
})

const report = {
  host: 'example.com',
  checked_at: '2026-06-01T12:00:00Z',
  status: 'valid',
  days_remaining: 60,
  trusted: true,
  hostname_match: true,
  tls_version: 'TLS 1.3',
  cipher_suite: 'TLS_AES_128_GCM_SHA256',
  problems: [],
  certificate: {
    subject_name: 'example.com', subject_org: 'Example Org', issuer_name: 'Test Root CA', issuer_org: 'Test Trust',
    not_before: '2026-05-02T12:00:00Z', not_after: '2026-07-31T12:00:00Z', dns_names: ['example.com', 'www.example.com'],
    serial_number: '1a2b', signature_algorithm: 'ECDSA-SHA256', key_algorithm: 'ECDSA', key_bits: 256, sha256_fingerprint: 'abcd'.repeat(16),
  },
  chain: [{ subject_name: 'example.com', issuer_name: 'Test Root CA', not_after: '2026-07-31T12:00:00Z' }],
}

function stubChecker(answer: () => Promise<Response>) {
  const calls: { url: string; init?: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    calls.push({ url, init })
    if (url.includes('/tools/ssl-check')) return answer()
    return Promise.resolve(new Response(JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'x' } }), { status: 401, headers: { 'Content-Type': 'application/json' } }))
  }))
  return calls
}
const ok = (body: unknown, status = 200) => () => Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
const failure = (status: number, code: string, message: string) => ok({ error: { code, message } }, status)

describe('SslCheckerPage', () => {
  it('asks for a domain and will not submit an empty one', () => {
    renderPage(<SslCheckerPage />)
    expect(screen.getByRole('heading', { level: 1, name: 'SSL Certificate Checker' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Check certificate' })).toBeDisabled()
  })

  it('sends the domain the visitor typed and shows the certificate', async () => {
    const user = userEvent.setup()
    const calls = stubChecker(ok(report))
    renderPage(<SslCheckerPage />)
    await user.type(screen.getByLabelText('Domain name'), 'https://Example.com/path')
    await user.click(screen.getByRole('button', { name: 'Check certificate' }))

    expect(await screen.findByRole('heading', { name: 'Certificate is valid' })).toBeInTheDocument()
    const post = calls.find((c) => c.url.includes('/tools/ssl-check'))!
    expect(post.init?.method).toBe('POST')
    expect(JSON.parse(post.init?.body as string)).toEqual({ host: 'https://Example.com/path' })
    expect(screen.getByText(/60 days left/)).toBeInTheDocument()
    expect(screen.getByText('Test Root CA', { selector: 'dd' })).toBeInTheDocument()
    expect(screen.getByText('example.com, www.example.com')).toBeInTheDocument()
    expect(screen.getByText('July 31, 2026')).toBeInTheDocument()
    expect(screen.getByText(/TLS 1\.3/)).toBeInTheDocument()
    expect(trackEvent).toHaveBeenCalledWith('tool_use', { tool: 'ssl-certificate-checker' })
    expect(trackEvent).toHaveBeenCalledWith('tool_result', { tool: 'ssl-certificate-checker', status: 'valid' }) // never the domain itself
  })

  it('lists what is wrong, errors before warnings, for an expired certificate', async () => {
    const user = userEvent.setup()
    stubChecker(ok({
      ...report, status: 'expired', days_remaining: -10,
      problems: [
        { code: 'weak_key', severity: 'warning', message: 'The certificate uses a 1024-bit RSA key.' },
        { code: 'expired', severity: 'error', message: 'The certificate expired on May 22, 2026 (10 days ago).' },
      ],
    }))
    renderPage(<SslCheckerPage />)
    await user.type(screen.getByLabelText('Domain name'), 'example.com')
    await user.click(screen.getByRole('button', { name: 'Check certificate' }))

    expect(await screen.findByRole('heading', { name: 'Certificate has expired' })).toBeInTheDocument()
    expect(screen.getByText(/Expired 10 days ago/)).toBeInTheDocument()
    const items = screen.getAllByRole('listitem').filter((li) => /expired on|1024-bit/.test(li.textContent ?? ''))
    expect(items.map((li) => li.textContent)).toEqual([expect.stringContaining('expired on May 22'), expect.stringContaining('1024-bit')])
    expect(screen.getByRole('heading', { name: 'Do not be caught out by the next expiry' })).toBeInTheDocument()
  })

  it('shows the server’s explanation when the domain cannot be checked, and clears an earlier result', async () => {
    const user = userEvent.setup()
    let answer = ok(report)
    stubChecker(() => answer())
    renderPage(<SslCheckerPage />)
    await user.type(screen.getByLabelText('Domain name'), 'example.com')
    await user.click(screen.getByRole('button', { name: 'Check certificate' }))
    await screen.findByRole('heading', { name: 'Certificate is valid' })

    answer = failure(422, 'HOST_NOT_FOUND', 'We could not find that domain. Check the spelling and try again.')
    await user.click(screen.getByRole('button', { name: 'Check certificate' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('We could not find that domain.')
    expect(screen.queryByRole('heading', { name: 'Certificate is valid' })).not.toBeInTheDocument()
  })

  it('shows the wait message for a rate limit, and a plain message when the network fails', async () => {
    const user = userEvent.setup()
    let answer = failure(429, 'HOST_RATE_LIMITED', 'That domain has been checked several times recently. Please try again in 8 minutes.')
    stubChecker(() => answer())
    renderPage(<SslCheckerPage />)
    await user.type(screen.getByLabelText('Domain name'), 'example.com')
    await user.click(screen.getByRole('button', { name: 'Check certificate' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('try again in 8 minutes')

    answer = () => Promise.reject(new TypeError('network down'))
    await user.click(screen.getByRole('button', { name: 'Check certificate' }))
    expect(await screen.findByText(/could not reach the checker/)).toBeInTheDocument()
  })
})

const site = {
  host: 'example.com', url: 'https://example.com/', checked_at: '2026-06-01T12:00:00Z', verdict: 'up', reason: 'ok',
  status_code: 200, response_time_ms: 1234, speed: 'moderate',
}

function stubSite(answer: () => Promise<Response>) {
  const calls: { url: string; init?: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    calls.push({ url, init })
    if (url.includes('/tools/site-check')) return answer()
    return Promise.resolve(new Response(JSON.stringify({ error: { code: 'UNAUTHENTICATED', message: 'x' } }), { status: 401, headers: { 'Content-Type': 'application/json' } }))
  }))
  return calls
}

async function checkSite(text = 'example.com') {
  const user = userEvent.setup()
  renderPage(<WebsiteStatusPage />)
  await user.type(screen.getByLabelText('Website'), text)
  await user.click(screen.getByRole('button', { name: 'Check website' }))
  return user
}

describe('WebsiteStatusPage', () => {
  it('asks for a website and will not submit an empty one', () => {
    renderPage(<WebsiteStatusPage />)
    expect(screen.getByRole('heading', { level: 1, name: 'Is My Website Down?' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Check website' })).toBeDisabled()
  })

  it('says a site is up, with its status code, response time and speed, and sends what was typed', async () => {
    const calls = stubSite(ok(site))
    await checkSite('https://Example.com/pricing')
    expect(await screen.findByRole('heading', { name: 'example.com is up' })).toBeInTheDocument()
    expect(screen.getByText(/answered with HTTP 200 OK in 1,234 ms/)).toBeInTheDocument()
    expect(screen.getByText('200 OK', { selector: 'dd' })).toBeInTheDocument()
    expect(screen.getByText(/Moderate/)).toBeInTheDocument()
    expect(screen.getByText('https://example.com/')).toBeInTheDocument()
    const post = calls.find((c) => c.url.includes('/tools/site-check'))!
    expect(post.init?.method).toBe('POST')
    expect(JSON.parse(post.init?.body as string)).toEqual({ host: 'https://Example.com/pricing' })
    expect(trackEvent).toHaveBeenCalledWith('tool_use', { tool: 'is-website-down' })
    expect(trackEvent).toHaveBeenCalledWith('tool_result', { tool: 'is-website-down', status: 'up' }) // never the domain
  })

  it('shows where a redirect ended', async () => {
    stubSite(ok({ ...site, final_url: 'https://www.example.com/home' }))
    await checkSite()
    expect(await screen.findByText('https://www.example.com/home')).toBeInTheDocument()
  })

  it('distinguishes a reachable site that refused the request from one that is down', async () => {
    stubSite(ok({ ...site, verdict: 'error_response', reason: 'http_4xx', status_code: 403, speed: 'fast', response_time_ms: 90 }))
    await checkSite()
    expect(await screen.findByRole('heading', { name: 'example.com is reachable, but it returned an error' })).toBeInTheDocument()
    expect(screen.getByText(/refuse automated requests/)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Be the first to know next time' })).toBeInTheDocument()
  })

  it.each([
    ['http_5xx', { status_code: 503, response_time_ms: 300, speed: 'fast' }, 'example.com is down', /HTTP 503 Service Unavailable/],
    ['dns', {}, 'We could not find example.com', /did not resolve/],
    ['connect', {}, 'example.com is not accepting connections', /did not accept a connection/],
    ['timeout', {}, 'example.com did not answer in time', /about six seconds/],
    ['redirect', {}, 'example.com redirects in a loop', /too many times/],
    ['tls', {}, 'example.com has a problem with its secure connection', /certificate problem/],
  ])('explains a %s failure', async (reason, extra, headline, detail) => {
    stubSite(ok({ host: 'example.com', url: 'https://example.com/', checked_at: site.checked_at, verdict: 'down', reason, ...extra }))
    await checkSite()
    expect(await screen.findByRole('heading', { name: headline as string })).toBeInTheDocument()
    expect(screen.getByText(detail as RegExp)).toBeInTheDocument()
  })

  it('points a certificate failure to the SSL checker, and warns when only plain HTTP works', async () => {
    stubSite(ok({ ...site, url: 'http://example.com/', https_error: 'tls' }))
    await checkSite()
    expect(await screen.findByText(/secure \(HTTPS\) version could not be reached/)).toBeInTheDocument()
  })

  it('shows no response time or status for a site that never answered', async () => {
    stubSite(ok({ host: 'example.com', url: 'https://example.com/', checked_at: site.checked_at, verdict: 'down', reason: 'connect' }))
    await checkSite()
    await screen.findByRole('heading', { name: /not accepting connections/ })
    expect(screen.queryByText('Status code')).not.toBeInTheDocument()
    expect(screen.queryByText('Response time')).not.toBeInTheDocument()
  })

  it('shows the server’s explanation for a refused domain or a limit, and a plain message when the network fails', async () => {
    let answer = failure(422, 'HOST_NOT_ALLOWED', 'We can only check publicly reachable websites.')
    stubSite(() => answer())
    const user = await checkSite('internal.example.com')
    expect(await screen.findByRole('alert')).toHaveTextContent('publicly reachable')
    answer = failure(429, 'HOST_RATE_LIMITED', 'That domain has been checked several times recently. Please try again in 8 minutes.')
    await user.click(screen.getByRole('button', { name: 'Check website' }))
    expect(await screen.findByText(/try again in 8 minutes/)).toBeInTheDocument()
    answer = () => Promise.reject(new TypeError('network down'))
    await user.click(screen.getByRole('button', { name: 'Check website' }))
    expect(await screen.findByText(/could not reach the checker/)).toBeInTheDocument()
  })
})
