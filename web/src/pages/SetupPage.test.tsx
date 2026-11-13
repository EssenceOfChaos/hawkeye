import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SetupPage, type MonitorSuggestion } from './SetupPage'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const s = (kind: MonitorSuggestion['kind'], url: string, o: Partial<MonitorSuggestion> = {}): MonitorSuggestion => ({
  kind, reason: `Reason for ${kind}.`, recommended: true, already_monitored: false,
  monitor: kind === 'mail' ? { type: 'dns', name: 'example.com mail (MX)', url, dns_record_type: 'MX' } : { type: 'http', name: url, url }, ...o,
})

function setup(opts: { suggestions: MonitorSuggestion[] | { status: number; body: unknown }; used?: number; limit?: number; onCreate?: (body: any) => Promise<Response> }) {
  document.cookie = 'csrf_token=t; path=/'
  const created: any[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/usage')) return json(200, { usage: { monitors_used: opts.used ?? 0, monitor_limit: opts.limit ?? 50, plan: {}, over_limit: false, next_plan: null } })
      if (url.endsWith('/monitors/suggestions')) {
        const sg = opts.suggestions
        return Array.isArray(sg) ? json(200, { domain: 'example.com', suggestions: sg }) : json(sg.status, sg.body)
      }
      if (url.endsWith('/monitors') && init?.method === 'POST') {
        const body = JSON.parse(init.body as string)
        created.push(body)
        return opts.onCreate?.(body) ?? json(201, { monitor: { id: `m${created.length}` } })
      }
      return json(404, {})
    }),
  )
  render(
    <MemoryRouter>
      <SetupPage />
    </MemoryRouter>,
  )
  return created
}

async function suggest(domain = 'example.com') {
  await userEvent.type(screen.getByLabelText(/Domain/), domain)
  await userEvent.click(screen.getByRole('button', { name: 'Suggest monitors' }))
}

describe('SetupPage', () => {
  it('preselects the recommended suggestions and creates exactly what was picked', async () => {
    const created = setup({
      suggestions: [
        s('website', 'https://example.com/'),
        s('health', 'https://example.com/healthz'),
        s('mail', 'example.com'),
      ],
    })
    await suggest()
    expect(await screen.findByText('Suggested for example.com')).toBeInTheDocument()
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes.map((b) => (b as HTMLInputElement).checked)).toEqual([true, true, true])

    await userEvent.click(boxes[1]) // the person leaves out the health endpoint
    await userEvent.click(screen.getByRole('button', { name: 'Add 2 monitors' }))

    expect(await screen.findByText('Added 2 monitors')).toBeInTheDocument()
    expect(created).toEqual([
      { type: 'http', name: 'https://example.com/', url: 'https://example.com/' },
      { type: 'dns', name: 'example.com mail (MX)', url: 'example.com', dns_record_type: 'MX' },
    ])
    expect(screen.getByRole('link', { name: 'Go to your monitors' })).toHaveAttribute('href', '/monitors')
  })

  it('leaves out what is not recommended or already monitored', async () => {
    setup({
      suggestions: [
        s('website', 'https://example.com/', { recommended: false, reason: 'Your home page answered 403 just now.' }),
        s('health', 'https://example.com/healthz', { already_monitored: true }),
      ],
    })
    await suggest()
    expect(await screen.findByText(/answered 403/)).toBeInTheDocument()
    const [home, health] = screen.getAllByRole('checkbox') as HTMLInputElement[]
    expect(home.checked).toBe(false)
    expect(home.disabled).toBe(false)
    expect(health.checked).toBe(false)
    expect(health.disabled).toBe(true)
    expect(screen.getByText(/already monitored/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add 0 monitors' })).toBeDisabled()
  })

  it('preselects no more than the plan has room for', async () => {
    setup({ suggestions: [s('website', 'https://example.com/'), s('health', 'https://example.com/healthz'), s('mail', 'example.com')], used: 4, limit: 5 })
    await suggest()
    expect(await screen.findByText(/room for 1 more monitor\./)).toBeInTheDocument()
    const boxes = screen.getAllByRole('checkbox') as HTMLInputElement[]
    expect(boxes.map((b) => b.checked)).toEqual([true, false, false])
    expect(boxes[1].disabled).toBe(true)
  })

  it('says why a monitor could not be added', async () => {
    setup({
      suggestions: [s('website', 'https://example.com/')],
      onCreate: () => json(403, { error: { code: 'PLAN_LIMIT_REACHED', message: 'Your plan allows 5 monitors.' } }),
    })
    await suggest()
    await userEvent.click(await screen.findByRole('button', { name: 'Add 1 monitor' }))
    expect(await screen.findByText('No monitors were added')).toBeInTheDocument()
    expect(screen.getByText(/Your plan allows 5 monitors\./)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'See plans' })).toBeInTheDocument()
  })

  it('shows a bad domain next to the field and other failures above', async () => {
    setup({ suggestions: { status: 400, body: { error: { code: 'INVALID_HOST', message: 'That does not look like a valid domain name.' } } } })
    await suggest('not a domain')
    expect(await screen.findByText('That does not look like a valid domain name.')).toBeInTheDocument()
  })
})
