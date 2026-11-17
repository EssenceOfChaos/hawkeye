import { useId, useState } from 'react'
import type { RequestHeader } from '../api'
import { trackEvent } from '../analytics'
import { parseCurl, type CurlRequest } from '../curl'
import { Alert } from './Alert'

interface Props {
  /** Fills the form (or a workflow step) from the command. */
  onImport: (r: CurlRequest) => void
  /** Puts back a header the import left out. */
  onRestore: (h: RequestHeader) => void
  /** Whether the form has a timeout field, so --max-time can be used. */
  timeout?: boolean
  /** Names what is being filled in for screen readers, such as "step 2". */
  target?: string
}

/**
 * "Paste a cURL command": reads the command in the browser and fills in the form. The pasted text never leaves the page and is cleared once read,
 * since it often holds a token. Afterward it lists what was moved, left out or ignored, and offers to put back headers that were left out.
 */
export function CurlImport({ onImport, onRestore, timeout, target }: Props) {
  const id = useId()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [last, setLast] = useState<CurlRequest | null>(null)
  const [restored, setRestored] = useState<string[]>([])

  function read() {
    const r = parseCurl(text, { timeout })
    if (!r.ok) {
      setError(r.error)
      return
    }
    onImport(r.request)
    trackEvent('import_curl', {
      method: r.request.method,
      auth: r.request.auth.type,
      headers: String(r.request.headers.length),
      omitted: String(r.request.omitted.length),
      warnings: String(r.request.warnings.length),
    })
    setLast(r.request)
    setRestored([])
    setText('')
    setError(null)
    setOpen(false)
  }

  const suffix = target ? ` for ${target}` : ''
  return (
    <div className="curl-import">
      {!open && (
        <button type="button" className="linklike" aria-label={`Paste a cURL command${suffix}`} onClick={() => setOpen(true)}>
          Paste a cURL command
        </button>
      )}
      {open && (
        <div className="field">
          <label htmlFor={`${id}-curl`}>cURL command{suffix}</label>
          <textarea id={`${id}-curl`} rows={5} className="mono" value={text} spellCheck={false} autoComplete="off"
            placeholder={"curl https://api.example.com/v1/status \\\n  -H 'Authorization: Bearer …'"}
            aria-invalid={error ? true : undefined} aria-describedby={`${id}-curl-hint`}
            onChange={(e) => {
              setText(e.target.value)
              setError(null)
            }} />
          <small id={`${id}-curl-hint`} className="hint">
            From your API docs, a terminal, or your browser’s developer tools (Copy as cURL). It’s read here in your browser; nothing is sent until you save.
          </small>
          {error && <small className="error">{error}</small>}
          <div className="row">
            <button type="button" onClick={read}>Fill in the form</button>
            <button type="button" className="linklike" onClick={() => {
              setOpen(false)
              setText('')
              setError(null)
            }}>Cancel</button>
          </div>
        </div>
      )}
      {last && !open && (
        <div className="curl-summary">
          {last.warnings.length > 0 ? (
            <Alert tone="warning">
              <strong>Filled in from your command, but check this first:</strong>
              <ul>{last.warnings.map((w, i) => <li key={i}><code>{w.subject}</code>: {w.text}</li>)}</ul>
            </Alert>
          ) : (
            <Alert tone="success">Filled in from your command. Check the fields, then save.</Alert>
          )}
          {(last.notes.length > 0 || last.omitted.length > 0) && (
            <details>
              <summary>What changed from the command</summary>
              <ul>
                {last.notes.map((n, i) => <li key={i}><code>{n.subject}</code>: {n.text}</li>)}
              </ul>
              {last.omitted.length > 0 && (
                <ul className="curl-omitted">
                  {last.omitted.map((h) => (
                    <li key={h.name}>
                      <code>{h.name}</code>{' '}
                      {restored.includes(h.name) ? <span className="hint">put back</span> : (
                        <button type="button" className="linklike" aria-label={`Put back the ${h.name} header${suffix}`}
                          onClick={() => {
                            onRestore(h)
                            setRestored((r) => [...r, h.name])
                          }}>Put back</button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </details>
          )}
          <button type="button" className="linklike" onClick={() => setLast(null)}>Dismiss</button>
        </div>
      )}
    </div>
  )
}
