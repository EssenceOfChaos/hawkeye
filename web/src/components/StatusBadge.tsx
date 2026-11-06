import type { Monitor } from '../api'

type Kind = 'up' | 'slow' | 'down' | 'paused' | 'maintenance' | 'unknown'

const LOOK: Record<Kind, { text: string; tip: string; dot: string }> = {
  up: { text: 'Online', tip: 'Active · Online', dot: 'd-status-success' },
  slow: { text: 'Slow', tip: 'Active · Online, but responding slower than its alert threshold', dot: 'd-status-warning' },
  down: { text: 'Offline', tip: 'Active · Offline', dot: 'd-status-error' },
  paused: { text: 'Paused', tip: 'Inactive · Paused', dot: 'd-status-warning' },
  maintenance: { text: 'Maintenance', tip: 'Active · In a maintenance window, no alerts', dot: 'd-status-info' },
  unknown: { text: 'Pending first check', tip: 'Active · Waiting for the first check', dot: 'd-status-neutral' },
}

/** A heartbeat (cron job) has no website to be online or offline: it is on time, or late or failing. */
const HEARTBEAT: Partial<Record<Kind, { text: string; tip: string }>> = {
  up: { text: 'On time', tip: 'Active · The job is pinging on schedule' },
  down: { text: 'Failing', tip: 'Active · A ping was missed, or the job reported a failure' },
  unknown: { text: 'Waiting for first ping', tip: 'Active · Add the ping to your job; no alerts until the first ping' },
}

/** A host name can answer perfectly well with the wrong record, so a DNS monitor says what it found rather than online or offline. */
const DNS: Partial<Record<Kind, { text: string; tip: string }>> = {
  up: { text: 'Resolving', tip: 'Active · The record resolves as expected' },
  down: { text: 'Failing', tip: 'Active · The record is missing or does not match' },
}

/**
 * A daisyUI status dot (green = online, yellow = paused/inactive, red = offline and pulsing) with a hover tooltip.
 * The text beside the dot always carries the meaning, so color and motion are never the only signal.
 */
export function StatusBadge({ monitor }: {
  monitor: Pick<Monitor, 'enabled' | 'current_status'> & { in_maintenance?: boolean; slow_since?: string | null; type?: Monitor['type'] }
}) {
  const kind: Kind = !monitor.enabled
    ? 'paused'
    : monitor.in_maintenance
      ? 'maintenance'
      : monitor.current_status === 'up'
        ? monitor.slow_since ? 'slow' : 'up'
        : monitor.current_status === 'down' ? 'down' : 'unknown'
  const look = { ...LOOK[kind], ...(monitor.type === 'heartbeat' ? HEARTBEAT[kind] : monitor.type === 'dns' ? DNS[kind] : undefined) }
  return (
    <span className={`badge ${kind}`}>
      <span className="d-tooltip" data-tip={look.tip}>
        <span className="status-stack" aria-hidden="true">
          {kind === 'down' && <span className={`d-status d-status-xl ${look.dot} animate-ping`} />}
          <span className={`d-status d-status-xl ${look.dot}`} />
        </span>
      </span>{' '}
      {look.text}
    </span>
  )
}
