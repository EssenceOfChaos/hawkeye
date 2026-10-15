import type { ReactNode } from 'react'

type Tone = 'error' | 'warning' | 'info' | 'success'

// Full class names (not built from the tone) so the CSS generator can see them.
const CLASS: Record<Tone, string> = {
  error: 'd-alert d-alert-soft d-alert-error',
  warning: 'd-alert d-alert-soft d-alert-warning',
  info: 'd-alert d-alert-soft d-alert-info',
  success: 'd-alert d-alert-soft d-alert-success',
}

/** A daisyUI alert. Errors are announced immediately (role="alert"); the other tones are polite status messages. */
export function Alert({ tone = 'error', role, children }: { tone?: Tone; role?: 'alert' | 'status' | 'note'; children: ReactNode }) {
  return (
    <div role={role ?? (tone === 'error' ? 'alert' : 'status')} className={CLASS[tone]}>
      <span>{children}</span>
    </div>
  )
}
