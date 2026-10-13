import { useMemo } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { RangeInfo, SeriesPoint } from '../api'
import { formatDateTime, formatMs } from '../format'

interface Props {
  title: string
  points: SeriesPoint[]
  bucketSeconds: number
  range: RangeInfo
  /** True while a refetch is in flight: the previous chart stays visible, dimmed. */
  loading?: boolean
}

interface Datum {
  t: number
  avg: number | null
  max: number | null
  checks: number
  failed: number
}

/** Insert empty points where whole buckets are missing so the line breaks instead of bridging gaps. */
export function withGaps(points: SeriesPoint[], bucketSeconds: number): Datum[] {
  const out: Datum[] = []
  const step = bucketSeconds * 1000
  points.forEach((p, i) => {
    const t = new Date(p.t).getTime()
    const prev = i > 0 ? new Date(points[i - 1].t).getTime() : null
    if (prev !== null && t - prev > step * 1.5) out.push({ t: prev + step, avg: null, max: null, checks: 0, failed: 0 })
    out.push({ t, avg: p.avg_ms, max: p.max_ms, checks: p.checks, failed: p.failed })
  })
  return out
}

function tickFormat(spanMs: number) {
  return (t: number) => {
    const d = new Date(t)
    return spanMs <= 2 * 86400000
      ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
      : d.toLocaleDateString([], { month: 'short', day: 'numeric' })
  }
}

interface TipProps {
  active?: boolean
  payload?: { payload: Datum }[]
}

/** Values lead (strong), names follow. All text uses text tokens; the line key is a short stroke in the series colour. */
function Tip({ active, payload }: TipProps) {
  const d = payload?.[0]?.payload
  if (!active || !d) return null
  return (
    <div className="chart-tip" role="status">
      <div className="tip-time">{formatDateTime(new Date(d.t).toISOString())}</div>
      <div className="tip-row">
        <span className="tip-key" aria-hidden="true" />
        <strong>{formatMs(d.avg)}</strong> <span>average</span>
      </div>
      <div className="tip-row sub"><strong>{formatMs(d.max)}</strong> <span>slowest</span></div>
      <div className="tip-row sub">
        <strong>{d.checks}</strong> <span>checks</span>
        {d.failed > 0 && <span className="tip-fail"> · <strong>{d.failed}</strong> failed</span>}
      </div>
    </div>
  )
}

export function ResponseChart({ title, points, bucketSeconds, range, loading }: Props) {
  const data = useMemo(() => withGaps(points, bucketSeconds), [points, bucketSeconds])
  const from = new Date(range.from).getTime()
  const to = new Date(range.to).getTime()
  const bucketLabel = bucketSeconds >= 3600 ? `${bucketSeconds / 3600} h` : `${bucketSeconds / 60} min`
  const failedTotal = points.reduce((n, p) => n + p.failed, 0)

  return (
    <figure className="chart" style={{ opacity: loading ? 0.55 : 1 }} aria-busy={loading || undefined}>
      <figcaption>
        <strong>{title}</strong>
        <span className="hint">
          {' '}
          · averages per {bucketLabel}
          {failedTotal > 0 && ` · ${failedTotal} failed check${failedTotal === 1 ? '' : 's'} in this period`}
        </span>
      </figcaption>

      {points.length === 0 ? (
        <p className="chart-empty">No checks have run in this period yet.</p>
      ) : (
        <div className="chart-plot" role="img" aria-label={`${title}. ${points.length} data points; a table view follows.`}>
          {/* Height includes the x-axis band, so the card never needs an inner scrollbar. */}
          <ResponsiveContainer width="100%" height={260}>
            <ComposedChart data={data} margin={{ top: 8, right: 12, bottom: 0, left: 0 }} accessibilityLayer>
              <CartesianGrid vertical={false} stroke="var(--chart-grid)" strokeWidth={1} />
              <XAxis
                dataKey="t"
                type="number"
                scale="time"
                domain={[from, to]}
                tickFormatter={tickFormat(to - from)}
                stroke="var(--chart-grid)"
                tick={{ fill: 'var(--chart-text)', fontSize: 12 }}
                tickLine={false}
                minTickGap={48}
              />
              <YAxis
                width={52}
                stroke="var(--chart-grid)"
                tick={{ fill: 'var(--chart-text)', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
                tickFormatter={(v: number) => (v >= 1000 ? `${v / 1000}s` : `${v}`)}
                label={{ value: 'ms', position: 'insideTopLeft', offset: 0, fill: 'var(--chart-text)', fontSize: 12 }}
              />
              <Tooltip content={<Tip />} cursor={{ stroke: 'var(--chart-text)', strokeWidth: 1 }} isAnimationActive={false} />
              <Area type="monotone" dataKey="avg" stroke="none" fill="var(--chart-line)" fillOpacity={0.1} connectNulls={false} isAnimationActive={false} />
              <Line
                type="monotone"
                dataKey="avg"
                stroke="var(--chart-line)"
                strokeWidth={2}
                strokeLinecap="round"
                strokeLinejoin="round"
                dot={false}
                activeDot={{ r: 4, fill: 'var(--chart-line)', stroke: 'var(--surface)', strokeWidth: 2 }}
                connectNulls={false}
                isAnimationActive={false}
              />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      )}

      {range.clamped_to_retention && (
        <p className="hint" role="note">
          Showing {range.retention_days} days: that is how much history your plan keeps.
        </p>
      )}

      {points.length > 0 && (
        <details className="table-view">
          <summary>View as table</summary>
          <div className="table-wrap">
            <table>
              <caption className="sr-only">{title}</caption>
              <thead>
                <tr><th>Period starting</th><th className="num">Average</th><th className="num">Slowest</th><th className="num">Checks</th><th className="num">Failed</th></tr>
              </thead>
              <tbody>
                {points.map((p) => (
                  <tr key={p.t}>
                    <td><time dateTime={p.t}>{formatDateTime(p.t)}</time></td>
                    <td className="num">{formatMs(p.avg_ms)}</td>
                    <td className="num">{formatMs(p.max_ms)}</td>
                    <td className="num">{p.checks}</td>
                    <td className="num">{p.failed}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      )}
    </figure>
  )
}
