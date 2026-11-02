import { describe, expect, it } from 'vitest'
import { badgeLink, snippets } from './badge'
import type { Plan, StatusPageConfig } from './api'

const plan = (code: string) => ({ code }) as Plan
const page = (o: Partial<StatusPageConfig>) => ({ id: 'p', slug: 'acme', title: 'Acme', description: '', published: true, monitors: [], accent_color: '', website_url: '', hide_powered_by: false, has_logo: false, logo_version: 0, ...o }) as StatusPageConfig

describe('badge embed code', () => {
  it('builds HTML and Markdown for both sizes, with or without a link', () => {
    const large = snippets('https://hawkeye.test', 'TOKEN', 'large', 'https://hawkeye.test/status/acme')
    expect(large.html).toBe('<a href="https://hawkeye.test/status/acme"><img src="https://hawkeye.test/badge/TOKEN.svg" alt="Uptime monitored by HawkEye" width="400" height="84"></a>')
    expect(large.markdown).toBe('[![Uptime monitored by HawkEye](https://hawkeye.test/badge/TOKEN.svg)](https://hawkeye.test/status/acme)')
    const small = snippets('https://hawkeye.test', 'TOKEN', 'small', null)
    expect(small.html).toBe('<img src="https://hawkeye.test/badge/TOKEN.svg?style=small" alt="Uptime monitored by HawkEye" width="200" height="28">')
    expect(small.markdown).toBe('![Uptime monitored by HawkEye](https://hawkeye.test/badge/TOKEN.svg?style=small)')
  })

  it('links Free badges to HawkEye, and paid ones to a published status page showing the monitor', () => {
    const o = 'https://hawkeye.test'
    expect(badgeLink(plan('free'), o, [], 'm1')).toBe('https://hawkeye.test/?utm_source=uptime_badge')
    expect(badgeLink(plan('pro'), o, [page({ monitors: [{ monitor_id: 'm2', display_name: '' }] })], 'm1')).toBeNull()
    expect(badgeLink(plan('pro'), o, [page({ published: false, monitors: [{ monitor_id: 'm1', display_name: '' }] })], 'm1')).toBeNull()
    expect(badgeLink(plan('agency'), o, [page({ slug: 'client-a', monitors: [{ monitor_id: 'm1', display_name: '' }] })], 'm1')).toBe('https://hawkeye.test/status/client-a')
  })
})
