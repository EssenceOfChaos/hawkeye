import { useMemo, type ReactNode } from 'react'
import { flattenJson, type JsonRow } from '../jsonRules'

/** A response as a test shows it. The monitor form's test request and multi-step checks' test runs both send this shape. */
export interface ShownResponse {
  body: string
  truncated: boolean
  binary: boolean
}

/**
 * A response body from a test. A JSON object or array is shown as a list of fields, and clicking one shows `actions` for it (add a check, save
 * the value...); anything else is shown as text. The response lives only in the caller's state and is never stored.
 */
export function ResponseBody({ response, picked, onPick, actions, hint, label = 'Response' }: {
  response: ShownResponse
  picked: JsonRow | null
  onPick: (r: JsonRow | null) => void
  /** What a clicked field offers. */
  actions: (row: JsonRow) => ReactNode
  /** Shown above a JSON response, such as "Click a field to check it." */
  hint: string
  /** Names the field list and text for screen readers ("Response", "Step 2 response"). */
  label?: string
}) {
  const doc = useMemo(() => {
    if (response.binary || response.truncated || response.body.trim() === '') return undefined
    try {
      const v: unknown = JSON.parse(response.body)
      return v !== null && typeof v === 'object' ? v : undefined
    } catch {
      return undefined
    }
  }, [response])
  const flat = useMemo(() => (doc === undefined ? null : flattenJson(doc)), [doc])

  if (response.binary) return <p className="hint">The response isn’t text, so it isn’t shown.</p>
  if (response.body === '') return <p className="hint">The response has no body.</p>
  if (!flat || flat.rows.length === 0) {
    return (
      <>
        <pre className="test-body" aria-label={`${label} body`}>{response.body}</pre>
        {response.truncated && <p className="hint">Only the start of the response is shown. Checks still read up to 1 MB of it.</p>}
      </>
    )
  }
  return (
    <>
      <p className="hint">{hint}</p>
      <ul className="json-tree" aria-label={`${label} fields`}>
        {flat.rows.map((row, i) => (
          <li key={i} style={{ paddingLeft: `${row.depth * 1.1}rem` }}>
            <button type="button" className="json-row" aria-pressed={picked === row} disabled={row.path === null}
              title={row.path === null ? 'This key has a dot or bracket in it, so a path can’t reach it.' : row.path}
              onClick={() => onPick(picked === row ? null : row)}>
              <span className="json-key">{row.label}</span>: <JsonValue row={row} />
            </button>
            {picked === row && (
              <div className="json-pick">
                <code>{row.path}</code>
                {actions(row)}
              </div>
            )}
          </li>
        ))}
      </ul>
      {flat.more && <p className="hint">Only the first {flat.rows.length.toLocaleString()} fields are shown.</p>}
    </>
  )
}

function JsonValue({ row }: { row: JsonRow }) {
  switch (row.kind) {
    case 'object':
      return <span className="hint">{`{${row.size} field${row.size === 1 ? '' : 's'}}`}</span>
    case 'array':
      return <span className="hint">{`[${row.size} item${row.size === 1 ? '' : 's'}]`}</span>
    case 'string':
      return <span className="json-string">{JSON.stringify(row.value)}</span>
    default:
      return <span className="json-literal">{String(row.value)}</span>
  }
}
