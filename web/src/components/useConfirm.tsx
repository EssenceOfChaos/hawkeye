import { useCallback, useRef, useState } from 'react'
import { ConfirmDialog, type ConfirmOptions } from './ConfirmDialog'

export type { ConfirmOptions }

interface Pending {
  id: number
  options: ConfirmOptions
  resolve: (ok: boolean) => void
}

/**
 * A branded replacement for window.confirm. `await confirm({...})` opens a modal dialog and resolves true only when the person presses the
 * confirm button; Cancel, the ✕, a click outside the box and Esc all resolve false. Render `dialog` somewhere in the component.
 *
 * Asking again while a dialog is open cancels the first question (it resolves false), so no caller is left waiting. Each question gets a
 * dialog of its own (keyed by id), so asking again straight after an answer never reuses the closed one.
 */
export function useConfirm() {
  const [pending, setPending] = useState<Pending | null>(null)
  const current = useRef<Pending | null>(null)
  const seq = useRef(0)

  const confirm = useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        current.current?.resolve(false)
        seq.current += 1
        const next = { id: seq.current, options, resolve }
        current.current = next
        setPending(next)
      }),
    [],
  )

  const dialog = pending && (
    <ConfirmDialog
      key={pending.id}
      {...pending.options}
      onDone={(ok) => {
        if (current.current === pending) current.current = null
        setPending((p) => (p === pending ? null : p)) // a newer question may already be showing
        pending.resolve(ok)
      }}
    />
  )
  return { confirm, dialog }
}
