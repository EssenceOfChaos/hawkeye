import { useState } from 'react'
import { ApiError, type Assertion, type RequestTest } from '../api'
import { timingsLine } from '../format'
import { describeRule, rulesFor, type JsonRow } from '../jsonRules'
import { ResponseBody } from './ResponseBody'
import { Alert } from './Alert'

/** What a test sent, kept with its result so the list still matches if the form changes afterward. */
export interface SentTest {
  result: RequestTest
  rules: Assertion[]
}

/**
 * "Send test request" for a website or API monitor: sends the form's request once, shows the response, and lists whether each check holds. In a
 * JSON response, clicking a field offers checks for it (`data.status equals "ok"`, `queue_size is at most 500`), which are added to the form. The
 * response lives only in this component's state: nothing is saved.
 */
export function TestRequest({ run, onAddRule, full }: {
  /** Sends the request as the form has it; throws an ApiError (field errors are shown on the form by the caller). */
  run: () => Promise<SentTest>
  onAddRule: (a: Assertion) => void
  /** The monitor already has as many checks as it may. */
  full: boolean
}) {
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState<SentTest | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [picked, setPicked] = useState<JsonRow | null>(null)
  const [added, setAdded] = useState('')

  async function send() {
    setBusy(true)
    setError(null)
    setPicked(null)
    setAdded('')
    try {
      setSent(await run())
    } catch (e) {
      setSent(null)
      setError(e instanceof ApiError ? e : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  function add(a: Assertion) {
    onAddRule(a)
    setAdded(`Added: ${describeRule(a)}.`)
    setPicked(null)
  }

  const fieldErrors = error && Object.keys(error.fields ?? {}).length > 0
  return (
    <fieldset className="group test-request">
      <legend>Test request</legend>
      <small className="hint">
        Send the request once to see the response, then click a field to check it. Nothing is saved, and the response isn’t stored.
      </small>
      <div>
        <button type="button" onClick={() => void send()} disabled={busy}>{busy ? 'Sending…' : 'Send test request'}</button>
      </div>
      {error && <Alert>{fieldErrors ? 'Fix the highlighted fields, then send the test again.' : error.message}</Alert>}
      {sent && <Outcome sent={sent} picked={picked} onPick={setPicked} onAdd={add} full={full} />}
      {added && <p role="status" className="hint">{added}</p>}
    </fieldset>
  )
}

function Outcome({ sent, picked, onPick, onAdd, full }: {
  sent: SentTest
  picked: JsonRow | null
  onPick: (r: JsonRow | null) => void
  onAdd: (a: Assertion) => void
  full: boolean
}) {
  const { result: r, response, checks } = sent.result
  const facts = [
    r.status_code != null && `HTTP ${r.status_code}`,
    r.response_time_ms != null && `${r.response_time_ms.toLocaleString()} ms`,
    r.content_type,
  ].filter(Boolean)
  return (
    <div className="test-outcome">
      <p className={r.success ? 'test-verdict up' : 'test-verdict down'}>
        <strong>{r.success ? 'The monitor would count as online.' : 'The monitor would count as offline.'}</strong>{' '}
        {!r.success && r.error_summary}
      </p>
      {facts.length > 0 && <p className="hint">{facts.join(' · ')}</p>}
      {r.timings && <p className="hint">{timingsLine(r.timings)}</p>}
      {checks.length > 0 && (
        <ul className="test-checks" aria-label="Checks">
          {checks.map((c, i) => (
            <li key={i} className={c.passed ? 'up' : 'down'}>
              <span aria-hidden="true">{c.passed ? '✓' : '✕'}</span> <span className="sr-only">{c.passed ? 'Passed: ' : 'Failed: '}</span>
              {sent.rules[i] ? describeRule(sent.rules[i]) : `Check ${i + 1}`}
              {!c.passed && c.message && <span className="hint">: {c.message}</span>}
            </li>
          ))}
        </ul>
      )}
      {response && (
        <ResponseBody response={response} picked={picked} onPick={onPick} hint="Click a field to check it."
          actions={(row) => (
            <>
              {rulesFor(row).map((a, j) => (
                <button key={j} type="button" className="value-chip" disabled={full} onClick={() => onAdd(a)}>
                  {describeRule(a).slice((row.path ?? '').length + 1)}
                </button>
              ))}
              {full && <small className="hint">This monitor has as many checks as it can have. Remove one to add another.</small>}
            </>
          )} />
      )}
    </div>
  )
}
