import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Monitor, Plan } from '../api'
import { BadgeCard } from './BadgeCard'

afterEach(() => vi.unstubAllGlobals())

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

const monitor = (badge_token: string | null) => ({ id: 'm1', name: 'Site', badge_token }) as Monitor

function setup(token: string | null, isOwner: boolean, code = 'pro') {
  document.cookie = 'csrf_token=t; path=/'
  const calls: string[] = []
  vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
    calls.push(`${init?.method ?? 'GET'} ${url}`)
    if (url.endsWith('/badge') && init?.method === 'POST') return json(200, { badge_token: 'NEWTOKEN' })
    if (url.endsWith('/status-pages')) return json(200, { status_pages: [], max: 3 })
    return json(404, {})
  }))
  render(<BadgeCard monitor={monitor(token)} plan={{ code } as Plan} isOwner={isOwner} />)
  return calls
}

describe('BadgeCard', () => {
  it('lets the owner turn the badge on and shows the code to embed it', async () => {
    const calls = setup(null, true)
    expect(screen.queryByLabelText('HTML')).not.toBeInTheDocument()
    await userEvent.click(screen.getByLabelText('Public uptime badge'))
    expect(await screen.findByLabelText('HTML')).toHaveValue(`<img src="${window.location.origin}/badge/NEWTOKEN.svg" alt="Uptime monitored by HawkEye" width="400" height="84">`)
    expect(calls).toContain('POST /api/v1/monitors/m1/badge')
    await userEvent.click(screen.getByLabelText('README (200 × 28)'))
    expect(screen.getByLabelText('Markdown')).toHaveValue(`![Uptime monitored by HawkEye](${window.location.origin}/badge/NEWTOKEN.svg?style=small)`)
    expect(screen.getByAltText('Preview of the uptime badge')).toHaveAttribute('src', `${window.location.origin}/badge/NEWTOKEN.svg?style=small`)
    await userEvent.click(screen.getByText('How do I add it?'))
    expect(screen.getByText(/add a “Custom HTML” block/)).toBeVisible()
    expect(screen.getByText(/paste it near the top of/)).toBeVisible()
  })

  it('confirms before turning it off', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const calls = setup('TOKEN', true)
    await userEvent.click(screen.getByLabelText('Public uptime badge'))
    expect(confirm).toHaveBeenCalled()
    expect(calls.some((c) => c.startsWith('DELETE'))).toBe(false)
    confirm.mockRestore()
  })

  it('shows members the code when the badge is on, but no switch', async () => {
    setup('TOKEN', false)
    expect(screen.queryByLabelText('Public uptime badge')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Markdown')).toBeInTheDocument()
  })

  it('tells members the owner can turn it on', async () => {
    setup(null, false)
    expect(screen.getByText(/owner can turn on a public badge/)).toBeInTheDocument()
  })

  it('links a Free badge to HawkEye', async () => {
    setup('TOKEN', true, 'free')
    expect((screen.getByLabelText('HTML') as HTMLInputElement).value).toContain(`href="${window.location.origin}/?utm_source=uptime_badge"`)
    expect(screen.getByText('On the Free plan the badge links to HawkEye.')).toBeInTheDocument()
  })
})
