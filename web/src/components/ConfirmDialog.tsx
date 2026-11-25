import { XMarkIcon } from '@heroicons/react/24/outline'
import { useEffect, useId, useRef, type ReactNode } from 'react'

export interface ConfirmOptions {
  /** The question, such as `Delete "Website"?`. */
  title: string
  /** What happens if the person goes ahead. */
  message: ReactNode
  /** The button that goes ahead, named for what it does ("Delete monitor"), never just "OK". */
  confirmLabel: string
  cancelLabel?: string
  /** A destructive action: the button is red, and Cancel has the focus so Enter can't delete by accident. */
  danger?: boolean
}

/** The dialog behind useConfirm (useConfirm.tsx). */
export function ConfirmDialog({ title, message, confirmLabel, cancelLabel = 'Cancel', danger = false, onDone }: ConfirmOptions & { onDone: (ok: boolean) => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLButtonElement>(null)
  const done = useRef(false)
  const id = useId()

  useEffect(() => {
    const d = ref.current
    if (!d) return
    // showModal puts the dialog in the top layer, makes the page behind it inert and closes it on Esc. Every browser the build targets has it;
    // the fallback is only for jsdom in tests, which lacks it. The open check matters under StrictMode, which runs this effect twice in
    // development: some older Safari versions throw when showModal is called on a dialog that is already open.
    if (typeof d.showModal === 'function') {
      if (!d.open) d.showModal()
    } else d.setAttribute('open', '')
    ;(danger ? cancelRef : confirmRef).current?.focus()
  }, [danger])

  function finish(ok: boolean) {
    if (done.current) return
    done.current = true
    // close() hands the focus back to the button that opened the dialog.
    const d = ref.current
    if (d?.open && typeof d.close === 'function') d.close()
    onDone(ok)
  }

  return (
    <dialog ref={ref} className="d-modal confirm-dialog" aria-labelledby={`${id}-title`} aria-describedby={`${id}-message`} onClose={() => finish(false)}>
      <div className="d-modal-box">
        <button type="button" className="confirm-close" aria-label="Close" onClick={() => finish(false)}>
          <XMarkIcon aria-hidden="true" />
        </button>
        <h2 id={`${id}-title`}>{title}</h2>
        <div id={`${id}-message`} className="confirm-message">{message}</div>
        <div className="d-modal-action">
          <button ref={cancelRef} type="button" onClick={() => finish(false)}>{cancelLabel}</button>
          <button ref={confirmRef} type="button" className={danger ? 'confirm-ok danger' : 'confirm-ok'} onClick={() => finish(true)}>{confirmLabel}</button>
        </div>
      </div>
      {/* daisyUI's backdrop: the area around the box. Clicking it cancels. Hidden from the keyboard and screen readers, which have Cancel and Esc. */}
      <div className="d-modal-backdrop" aria-hidden="true" onClick={() => finish(false)} />
    </dialog>
  )
}
