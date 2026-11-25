import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { StatusPageDomain } from '../api'
import { CustomDomainCard } from './CustomDomain'
import { answerConfirm } from '../testConfirm'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const domain = (o: Partial<StatusPageDomain> = {}): StatusPageDomain => ({
  id: 'd1', page_id: 'p1', hostname: 'status.client.com', state: 'pending', last_error: '', last_checked_at: null, next_check_at: null,
  verified_at: null, failing_since: null, created_at: '2026-11-15T12:00:00Z',
  records: [
    { type: 'CNAME', name: 'status.client.com', value: 'domains.hawkeye.observer' },
    { type: 'TXT', name: '_hawkeye.status.client.com', value: 'hawkeye-verify=abcdefghijklmnopqrstuvwxyz234567' },
  ],
  ...o,
})
const view = (d: StatusPageDomain | null, max = 50) => ({ domain: d, target: 'domains.hawkeye.observer', max })

type Route = (m: string, url: string, body?: string) => Promise<Response> | undefined

function renderCard(route: Route, isOwner = true) {
  document.cookie = 'csrf_token=t; path=/'
  const calls: { m: string; url: string; body?: string }[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    const m = init?.method ?? 'GET'
    calls.push({ m, url, body: init?.body as string | undefined })
    return route(m, url, init?.body as string | undefined) ?? json(404, {})
  }))
  const onChange = vi.fn()
  render(<MemoryRouter><CustomDomainCard pageID="p1" isOwner={isOwner} onChange={onChange} /></MemoryRouter>)
  return { calls, onChange }
}

describe('CustomDomainCard', () => {
  it('offers the feature as an Agency upgrade on other plans', async () => {
    renderCard(() => json(200, view(null, 0)))
    expect(await screen.findByText(/Included in\s+the Agency plan/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Upgrade' })).toHaveAttribute('href', '/billing')
    expect(screen.queryByLabelText('Domain')).not.toBeInTheDocument()
  })

  it('sets up a domain and shows the two DNS records to copy', async () => {
    const { calls, onChange } = renderCard((m) => (m === 'PUT' ? json(200, view(domain())) : json(200, view(null))))
    await userEvent.type(await screen.findByLabelText('Domain'), 'status.client.com')
    await userEvent.click(screen.getByRole('button', { name: 'Set up domain' }))
    expect(await screen.findByText('Now add the two DNS records for status.client.com.')).toBeInTheDocument()
    expect(JSON.parse(calls.find((c) => c.m === 'PUT')!.body!)).toEqual({ hostname: 'status.client.com' })
    expect(screen.getByText('Waiting for DNS records')).toBeInTheDocument()
    expect(screen.getByLabelText('CNAME value')).toHaveValue('domains.hawkeye.observer')
    expect(screen.getByLabelText('TXT name')).toHaveValue('_hawkeye.status.client.com')
    expect(screen.getByLabelText('TXT value')).toHaveValue('hawkeye-verify=abcdefghijklmnopqrstuvwxyz234567')
    expect(onChange).toHaveBeenLastCalledWith('status.client.com', 'pending')
  })

  it('shows why a name was refused next to the field', async () => {
    renderCard((m) => m === 'PUT'
      ? json(400, { error: { code: 'VALIDATION_ERROR', message: 'One or more fields are invalid.', details: { fields: { hostname: 'Use a subdomain, such as status.client.com.' } } } })
      : json(200, view(null)))
    await userEvent.type(await screen.findByLabelText('Domain'), 'client.com')
    await userEvent.click(screen.getByRole('button', { name: 'Set up domain' }))
    expect(await screen.findByText('Use a subdomain, such as status.client.com.')).toBeInTheDocument()
  })

  it('says what is wrong, and goes live when Check now finds the records', async () => {
    const { calls } = renderCard((m, url) => {
      if (m === 'POST' && url.endsWith('/domain/check')) return json(200, view(domain({ state: 'active', verified_at: '2026-11-15T12:05:00Z' })))
      return json(200, view(domain({ last_error: 'The TXT record _hawkeye.status.client.com wasn’t found.', last_checked_at: '2026-11-15T12:01:00Z' })))
    })
    expect(await screen.findByText(/The TXT record _hawkeye.status.client.com wasn’t found/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Check now' }))
    expect(await screen.findByText(/the page is live on this domain/)).toBeInTheDocument()
    expect(screen.getByText('Live')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'status.client.com' })).toHaveAttribute('href', 'https://status.client.com/')
    expect(calls.some((c) => c.m === 'POST')).toBe(true)
  })

  it('removes the domain after asking', async () => {
    const { calls, onChange } = renderCard((m) => (m === 'DELETE' ? Promise.resolve(new Response(null, { status: 204 })) : json(200, view(domain({ state: 'active' })))))
    await userEvent.click(await screen.findByRole('button', { name: 'Remove' }))
    await answerConfirm('Remove domain')
    expect(await screen.findByText(/status.client.com was removed/)).toBeInTheDocument()
    expect(calls.some((c) => c.m === 'DELETE')).toBe(true)
    expect(onChange).toHaveBeenLastCalledWith('', '')
    expect(screen.getByLabelText('Domain')).toBeInTheDocument()
  })

  it('shows a member the domain and its records, without the owner’s buttons', async () => {
    renderCard(() => json(200, view(domain({ state: 'failing', last_error: 'status.client.com points somewhere other than HawkEye.' }))), false)
    expect(await screen.findByText('Needs attention')).toBeInTheDocument()
    expect(screen.getByText(/points somewhere other than HawkEye/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Check now' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument()
  })
})
