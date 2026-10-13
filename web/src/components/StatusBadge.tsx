import type { Monitor } from '../api'

type Kind = 'up' | 'down' | 'paused' | 'unknown'

const LOOK: Record<Kind, { text: string; tip: string; dot: string }> = {
  up: { text: 'Up', tip: 'Active · Up', dot: 'd-status-success' },
  down: { text: 'Down', tip: 'Active · Down', dot: 'd-status-error' },
  paused: { text: 'Paused', tip: 'Inactive · Paused', dot: 'd-status-warning' },
  unknown: { text: 'Pending first check', tip: 'Active · Waiting for the first check', dot: 'd-status-neutral' },
}

/**
 * A daisyUI status dot (green = up, yellow = paused/inactive, red = down and pulsing) with a hover tooltip.
 * The text beside the dot always carries the meaning, so colour and motion are never the only signal.
 */
export function StatusBadge({ monitor }: { monitor: Pick<Monitor, 'enabled' | 'current_status'> }) {
  const kind: Kind = !monitor.enabled ? 'paused' : monitor.current_status === 'up' ? 'up' : monitor.current_status === 'down' ? 'down' : 'unknown'
  const look = LOOK[kind]
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
