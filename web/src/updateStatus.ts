import type { IncidentUpdateStatus } from './api'

// Full class names so the CSS generator can see them. Shared by the incident editor and the public status page.
export const UPDATE_STATUS: Record<IncidentUpdateStatus, { label: string; cls: string }> = {
  investigating: { label: 'Investigating', cls: 'd-badge d-badge-soft d-badge-warning' },
  identified: { label: 'Identified', cls: 'd-badge d-badge-soft d-badge-error' },
  monitoring: { label: 'Monitoring', cls: 'd-badge d-badge-soft d-badge-info' },
  resolved: { label: 'Resolved', cls: 'd-badge d-badge-soft d-badge-success' },
}
