import { useCallback, useState } from 'react'
import { ConfirmDialog, type ConfirmOptions } from './ConfirmDialog'

export type { ConfirmOptions }

interface Pending {
  options: ConfirmOptions
  resolve: (ok: boolean) => void
}

/**
 * A branded replacement for window.confirm. `await confirm({...})` opens a modal dialog and resolves true only when the person presses the
 * confirm button; Cancel, the ✕, a click outside the box and Esc all resolve false. Render `dialog` somewhere in the component.
 */
export function useConfirm() {
  const [pending, setPending] = useState<Pending | null>(null)
  const confirm = useCallback((options: ConfirmOptions) => new Promise<boolean>((resolve) => setPending({ options, resolve })), [])
  const dialog = pending && (
    <ConfirmDialog
      {...pending.options}
      onDone={(ok) => {
        setPending(null)
        pending.resolve(ok)
      }}
    />
  )
  return { confirm, dialog }
}
