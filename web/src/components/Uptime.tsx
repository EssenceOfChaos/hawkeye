import type { Uptime } from '../api'
import { formatMs, formatUptime } from '../format'

/** The one hero figure of a view: check-based uptime, always with its window and sample size. */
export function UptimeHero({ uptime, label }: { uptime: Uptime; label: string }) {
  return (
    <section className="hero-figure" aria-label="Uptime">
      <div className="stat-label">Check-based uptime · {label}</div>
      {uptime.uptime_percent !== null ? (
        <div className="hero-value">{formatUptime(uptime.uptime_percent)}</div>
      ) : (
        <div className="hero-value muted" aria-label="Not enough data">—</div>
      )}
      <div className="hint">
        {uptime.insufficient_data
          ? `Not enough data yet: ${uptime.total_checks} of ${uptime.min_checks} checks needed.`
          : `${uptime.successful_checks.toLocaleString()} of ${uptime.total_checks.toLocaleString()} completed checks succeeded` +
            (uptime.failed_checks ? ` · ${uptime.failed_checks.toLocaleString()} failed` : '')}
      </div>
      <details className="explain">
        <summary>How is this calculated?</summary>
        <p>
          Uptime = successful checks ÷ completed checks × 100 over the selected period. It measures the checks we ran, not every
          second: periods when a monitor was paused, or when a check did not run or finish, are not counted either way. The figure is
          truncated, never rounded up, so it only reads 100% when every completed check succeeded. Fewer than {uptime.min_checks} checks
          is too little data to show a percentage.
        </p>
      </details>
    </section>
  )
}

export function StatTile({ label, value, hint }: { label: string; value: React.ReactNode; hint?: string }) {
  return (
    <div className="d-stats">
      <div className="d-stat">
        <div className="d-stat-title">{label}</div>
        <div className="d-stat-value">{value}</div>
        {hint && <div className="d-stat-desc">{hint}</div>}
      </div>
    </div>
  )
}

export function AvgResponseTile({ uptime }: { uptime: Uptime }) {
  return <StatTile label="Average response time" value={formatMs(uptime.avg_response_ms)} hint="Successful checks only" />
}
