import type { Plan, StatusPageConfig } from './api'

export type BadgeSize = 'large' | 'small'

export const BADGE_SIZES: Record<BadgeSize, { label: string; width: number; height: number; query: string }> = {
  large: { label: 'Website (400 × 84)', width: 400, height: 84, query: '' },
  small: { label: 'README (200 × 28)', width: 200, height: 28, query: '?style=small' },
}

/** The address an embedded badge links to: HawkEye on Free (badges there are a small thank-you), otherwise a public status page that shows the monitor. */
export function badgeLink(plan: Plan | undefined, origin: string, pages: StatusPageConfig[], monitorID: string): string | null {
  if (!plan || plan.code === 'free') return `${origin}/?utm_source=uptime_badge`
  const page = pages.find((p) => p.published && p.monitors.some((m) => m.monitor_id === monitorID))
  return page ? `${origin}/status/${encodeURIComponent(page.slug)}` : null
}

export function snippets(origin: string, token: string, size: BadgeSize, link: string | null) {
  const s = BADGE_SIZES[size]
  const src = `${origin}/badge/${token}.svg${s.query}`
  const alt = 'Uptime monitored by HawkEye'
  const img = `<img src="${src}" alt="${alt}" width="${s.width}" height="${s.height}">`
  return {
    src,
    html: link ? `<a href="${link}">${img}</a>` : img,
    markdown: link ? `[![${alt}](${src})](${link})` : `![${alt}](${src})`,
  }
}
