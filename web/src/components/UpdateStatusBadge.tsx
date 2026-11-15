import type { IncidentUpdateStatus } from '../api'
import { UPDATE_STATUS } from '../updateStatus'

/** The status of an update, as a badge. An unknown status (from a newer server) shows as its own name. */
export function UpdateStatusBadge({ status }: { status: string }) {
  const s = UPDATE_STATUS[status as IncidentUpdateStatus]
  return <span className={s?.cls ?? 'd-badge d-badge-soft'}>{s?.label ?? status}</span>
}
