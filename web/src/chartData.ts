import type { SeriesPoint } from './api'

export interface Datum {
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
