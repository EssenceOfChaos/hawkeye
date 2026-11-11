// The status page's HTML. Self-contained (inline styles, no fonts, images or scripts from anywhere), so it renders when HawkEye's own server is
// down. Everything that comes from the database is escaped.

import { days, duration, percent, uptime, type IncidentStatus, type Outage } from './logic'

export interface Snapshot {
  now: number
  measuredSince: number | null
  components: { id: string; name: string; description: string; downSince: number | null; outages: Outage[] }[]
  incidents: {
    id: number
    title: string
    created_at: number
    resolved_at: number | null
    updates: { status: IncidentStatus; message: string; created_at: number }[]
  }[]
}

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)

const day = (t: number) => new Date(t * 1000).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
const stamp = (t: number) =>
  `${new Date(t * 1000).toLocaleString('en-US', { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit', timeZone: 'UTC' })} UTC`

const statusWords: Record<IncidentStatus, string> = {
  investigating: 'Investigating', identified: 'Identified', monitoring: 'Monitoring', resolved: 'Resolved',
}

const BAR_DAYS = 90

function banner(s: Snapshot): { cls: string; text: string } {
  const down = s.components.filter((c) => c.downSince !== null)
  if (s.measuredSince === null) return { cls: 'none', text: 'Measuring has just started' }
  if (down.length === 0) return { cls: 'up', text: 'All systems operational' }
  if (down.length === s.components.length) return { cls: 'major', text: 'Major outage' }
  return { cls: 'minor', text: `${down.map((c) => c.name).join(' and ')}: outage` }
}

function component(s: Snapshot, c: Snapshot['components'][number]): string {
  const up90 = uptime(c.outages, s.now - BAR_DAYS * 86_400, s.now, s.measuredSince ?? s.now)
  const bars = days(c.outages, BAR_DAYS, s.now, s.measuredSince)
    .map((d) => {
      const label = d.status === 'none' ? `${day(d.start)}: not measured yet`
        : d.status === 'up' ? `${day(d.start)}: no downtime` : `${day(d.start)}: down ${duration(d.downSeconds)}`
      return `<li class="${d.status}" title="${esc(label)}"><span class="sr">${esc(label)}</span></li>`
    })
    .join('')
  const state = c.downSince !== null
    ? `<span class="state major">Down since ${esc(stamp(c.downSince))}</span>`
    : `<span class="state up">Operational</span>`
  return `<section class="component" aria-labelledby="c-${c.id}">
  <div class="row"><h2 id="c-${c.id}">${esc(c.name)}</h2>${state}</div>
  <p class="hint">${esc(c.description)}</p>
  <ol class="bars" aria-label="${esc(c.name)}, each of the last ${BAR_DAYS} days">${bars}</ol>
  <div class="row hint"><span>${BAR_DAYS} days ago</span><span>${esc(percent(up90))} uptime</span><span>Today</span></div>
</section>`
}

function incident(i: Snapshot['incidents'][number]): string {
  const updates = i.updates
    .map((u) => `<li><strong>${statusWords[u.status]}</strong> <time datetime="${new Date(u.created_at * 1000).toISOString()}">${esc(stamp(u.created_at))}</time><p>${esc(u.message)}</p></li>`)
    .join('')
  return `<article class="incident${i.resolved_at === null ? ' open' : ''}">
  <h3>${esc(i.title)}</h3>
  <ol class="updates">${updates}</ol>
</article>`
}

export function renderPage(s: Snapshot): string {
  const b = banner(s)
  const open = s.incidents.filter((i) => i.resolved_at === null)
  const past = s.incidents.filter((i) => i.resolved_at !== null)
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>HawkEye status</title>
<meta name="description" content="Whether HawkEye is up, measured every minute from outside HawkEye's own servers, with its uptime over the last 90 days and any incidents.">
<meta http-equiv="refresh" content="60">
<style>
:root { --navy: #091c33; --panel: #0f2744; --text: #eaf1fa; --muted: #a9b9cd; --gold: #e8b95e; --up: #56d364; --minor: #f0b429; --major: #ff8a80; --none: #2a4262; }
* { box-sizing: border-box; }
body { margin: 0; background: var(--navy); color: var(--text); font: 16px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 52rem; margin: 0 auto; padding: 1.5rem 1rem 3rem; }
header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: 1.5rem; }
.brand { color: var(--text); text-decoration: none; font-weight: 700; font-size: 1.35rem; letter-spacing: .01em; }
.brand b { color: var(--gold); }
a { color: var(--gold); }
h1 { font-size: 1.1rem; margin: 0; padding: 1rem 1.25rem; border-radius: .6rem; }
h1.up { background: #123d2a; } h1.minor { background: #4a3a12; } h1.major { background: #4d1f22; } h1.none { background: var(--panel); }
h2 { font-size: 1.05rem; margin: 0; }
.component, .incident, .about { background: var(--panel); border-radius: .6rem; padding: 1rem 1.25rem; margin-top: 1rem; }
.row { display: flex; justify-content: space-between; align-items: baseline; gap: .75rem; flex-wrap: wrap; }
.hint { color: var(--muted); font-size: .875rem; margin: .25rem 0; }
.state { font-size: .9rem; font-weight: 600; } .state.up { color: var(--up); } .state.major { color: var(--major); }
.bars { display: flex; gap: 2px; list-style: none; padding: 0; margin: .75rem 0 .25rem; height: 2.25rem; }
.bars li { flex: 1; border-radius: 2px; background: var(--none); }
.bars .up { background: var(--up); } .bars .minor { background: var(--minor); } .bars .major { background: var(--major); }
.sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
section.incidents h2 { margin-top: 2rem; }
.incident h3 { margin: 0 0 .5rem; font-size: 1rem; } .incident.open { border-left: 4px solid var(--minor); }
.updates { list-style: none; padding: 0; margin: 0; } .updates li + li { margin-top: .75rem; }
.updates time { color: var(--muted); font-size: .85rem; margin-left: .35rem; } .updates p { margin: .2rem 0 0; white-space: pre-line; }
footer { margin-top: 2rem; color: var(--muted); font-size: .85rem; }
@media (max-width: 480px) { .bars { gap: 1px; } }
</style>
</head>
<body>
<main>
<header>
  <a class="brand" href="https://hawkeye.observer/">Hawk<b>Eye</b> <span style="font-weight:400;color:var(--muted)">status</span></a>
  <a href="https://hawkeye.observer/contact">Contact support</a>
</header>
<h1 class="${b.cls}">${esc(b.text)}</h1>
${open.map(incident).join('\n')}
${s.components.map((c) => component(s, c)).join('\n')}
<section class="incidents" aria-labelledby="past-h">
  <h2 id="past-h">Past incidents</h2>
  ${past.length ? past.map(incident).join('\n') : '<p class="hint">No incidents in the last 30 days.</p>'}
</section>
<section class="about">
  <h2>How this is measured</h2>
  <p class="hint">
    This page runs separately from HawkEye, so it stays up when HawkEye doesn't. Every minute it loads HawkEye’s website and API, and HawkEye’s
    monitoring engine reports that checks are running on time. A component counts as down after two failures in a row, from the first of them.
    ${s.measuredSince ? `Measured since ${esc(day(s.measuredSince))}.` : ''}
  </p>
</section>
<footer>Updated ${esc(stamp(s.now))}. The page refreshes every minute.</footer>
</main>
</body>
</html>`
}
