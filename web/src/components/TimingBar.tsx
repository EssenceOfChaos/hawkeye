import { useState } from 'react'
import type { AvgTimings } from '../api'

/** The phases of a check, in the order they happen. Colors are the --phase-* tokens in index.css (validated as a set on the chart surface). */
const PHASES = [
  { key: 'dns', name: 'DNS lookup', hint: 'Looking up the host name' },
  { key: 'connect', name: 'Connect', hint: 'Opening the connection to the server' },
  { key: 'tls', name: 'TLS handshake', hint: 'Setting up the encrypted connection' },
  { key: 'wait', name: 'Server wait', hint: 'Time to first byte: from sending the request to the first byte of the answer, mostly the application’s own work' },
  { key: 'other', name: 'Other', hint: 'HawkEye’s own handling of the request and any redirects' },
] as const

type PhaseKey = (typeof PHASES)[number]['key']

interface Part { key: PhaseKey; name: string; hint: string; ms: number; share: number }

function parts(t: AvgTimings): Part[] {
  const values: Record<PhaseKey, number> = { dns: t.dns_ms, connect: t.connect_ms, tls: t.tls_ms, wait: t.wait_ms, other: t.other_ms ?? 0 }
  const total = Object.values(values).reduce((a, b) => a + b, 0)
  return PHASES.map((p) => ({ ...p, ms: values[p.key], share: total > 0 ? values[p.key] / total : 0 })).filter((p) => p.ms > 0)
}

const pct = (share: number) => `${Math.round(share * 100)}%`
const ms = (v: number) => `${v.toLocaleString()} ms`

/**
 * "Where the time goes": the average check's time split into its phases, as one tall segmented bar (a part-to-whole figure, so no axis), with a
 * legend in block order and a table view. Each block prints its share when it is wide enough, and hover or keyboard focus gives its name, time
 * and share.
 */
export function TimingBar({ timings, loading }: { timings: AvgTimings; loading?: boolean }) {
  const [active, setActive] = useState<Part | null>(null)
  const ps = parts(timings)
  if (ps.length === 0) return null
  const total = ps.reduce((a, p) => a + p.ms, 0)
  const summary = ps.map((p) => `${p.name} ${ms(p.ms)} (${pct(p.share)})`).join(', ')
  return (
    <figure className="chart timing" style={{ opacity: loading ? 0.55 : 1 }} aria-busy={loading || undefined}>
      <figcaption>
        <strong>Where the time goes</strong>
        <span className="hint">
          {' '}· average of {timings.checks.toLocaleString()} successful check{timings.checks === 1 ? '' : 's'}, {ms(total)} in all
        </span>
      </figcaption>
      <div className="timing-bar" role="img" aria-label={`Where the time goes, on average: ${summary}. A table view follows.`}>
        {ps.map((p) => (
          <div key={p.key} className={`timing-part phase-${p.key}`} style={{ flexGrow: p.ms }} tabIndex={0} aria-label={`${p.name}: ${ms(p.ms)}, ${pct(p.share)}`}
            onMouseEnter={() => setActive(p)} onMouseLeave={() => setActive(null)} onFocus={() => setActive(p)} onBlur={() => setActive(null)}>
            {p.share >= 0.09 && <span aria-hidden="true">{pct(p.share)}</span>}
          </div>
        ))}
      </div>
      <p className="timing-tip hint" aria-live="polite">
        {active ? <><strong>{active.name}</strong>: {ms(active.ms)} ({pct(active.share)}). {active.hint}.</> : ' '}
      </p>
      <ul className="timing-legend">
        {ps.map((p) => (
          <li key={p.key}><span className={`swatch phase-${p.key}`} aria-hidden="true" />{p.name} <span className="hint">{ms(p.ms)}</span></li>
        ))}
      </ul>
      <details className="table-view">
        <summary>View as table</summary>
        <div className="table-wrap">
          <table>
            <caption className="sr-only">Where the time goes, on average</caption>
            <thead><tr><th>Phase</th><th className="num">Average</th><th className="num">Share</th><th>What it is</th></tr></thead>
            <tbody>
              {ps.map((p) => (
                <tr key={p.key}><td>{p.name}</td><td className="num">{ms(p.ms)}</td><td className="num">{pct(p.share)}</td><td>{p.hint}</td></tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}
