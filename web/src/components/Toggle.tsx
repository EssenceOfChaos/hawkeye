import { useRef, type ReactNode } from 'react'
import { shake } from '../motion'

/**
 * A labeled on/off switch (daisyUI toggle): use it for a setting, and a plain checkbox for picking items from a list.
 * `onToggle` may return false, or a promise of false, when the change did not stick: the switch then shakes to show it went back.
 */
export function Toggle({ checked, disabled, busy, className, onToggle, children }: {
  checked: boolean
  disabled?: boolean
  busy?: boolean
  className?: string
  onToggle: (on: boolean) => void | boolean | Promise<void | boolean>
  children: ReactNode
}) {
  const input = useRef<HTMLInputElement>(null)
  return (
    <label className={className}>
      <input ref={input} type="checkbox" className="d-toggle d-toggle-sm d-toggle-primary" checked={checked} disabled={disabled || busy}
        onChange={(e) => {
          void Promise.resolve(onToggle(e.target.checked)).then((ok) => {
            if (ok === false && input.current) shake(input.current)
          })
        }} />{' '}
      {children}
      {busy && <> <span className="d-loading d-loading-spinner d-loading-xs" aria-hidden="true" /></>}
    </label>
  )
}
