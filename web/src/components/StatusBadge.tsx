import type { Monitor } from '../api'

/** Status is conveyed by an icon and text, never by colour alone. */
export function StatusBadge({ monitor }: { monitor: Pick<Monitor, 'enabled' | 'current_status'> }) {
  let icon: string, text: string, cls: string
  if (!monitor.enabled) [icon, text, cls] = ['⏸', 'Paused', 'paused']
  else if (monitor.current_status === 'up') [icon, text, cls] = ['✔', 'Up', 'up']
  else if (monitor.current_status === 'down') [icon, text, cls] = ['✖', 'Down', 'down']
  else [icon, text, cls] = ['○', 'Pending first check', 'unknown']
  return (
    <span className={`badge ${cls}`}>
      <span aria-hidden="true">{icon}</span> {text}
    </span>
  )
}
