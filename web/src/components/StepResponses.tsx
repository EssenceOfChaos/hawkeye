import { useState } from 'react'
import type { Assertion, StepResponse } from '../api'
import { describeRule, rulesFor, valueNameFor, type JsonRow } from '../jsonRules'
import type { StepDraft } from '../workflowDrafts'
import { ResponseBody } from './ResponseBody'

/** The server allows this many checks, and this many saved values, per step. */
const MAX_PER_STEP = 10

/**
 * Each tested step's response, under Run test in the multi-step editor. Clicking a field of a JSON response offers checks for that step and, for a
 * single value, saving it for later steps (`data.token` saved as `token`, used as {{token}}). Additions go to the step by its key, so they land on
 * the right step even if steps were moved since the test; a step removed since then takes nothing. Responses come with secrets and saved values
 * already hidden, and live only in the editor's state.
 */
export function StepResponses({ responses, names, stepKeys, rules, steps, onSteps }: {
  responses: (StepResponse | null)[]
  /** The steps' names, keys and checks when the test ran. */
  names: string[]
  stepKeys: number[]
  rules: Assertion[][]
  steps: StepDraft[]
  onSteps: (s: StepDraft[]) => void
}) {
  const [picked, setPicked] = useState<{ step: number; row: JsonRow } | null>(null)
  const [done, setDone] = useState('')
  if (!responses.some(Boolean)) return null

  const update = (key: number, patch: (s: StepDraft) => Partial<StepDraft>) =>
    onSteps(steps.map((s) => (s.key === key ? { ...s, ...patch(s) } : s)))
  const taken = steps.flatMap((s) => s.extractions.map((x) => x.name.trim()))

  return (
    <div className="step-responses">
      {responses.map((resp, i) => {
        if (!resp) return null
        const n = i + 1
        const label = `Step ${n} response`
        const step = steps.find((s) => s.key === stepKeys[i])
        return (
          <details key={i} className="step-response">
            <summary>{label}: {names[i] || `Step ${n}`}</summary>
            {resp.checks.length > 0 && (
              <ul className="test-checks" aria-label={`Step ${n} checks`}>
                {resp.checks.map((c, j) => (
                  <li key={j} className={c.passed ? 'up' : 'down'}>
                    <span aria-hidden="true">{c.passed ? '✓' : '✕'}</span> <span className="sr-only">{c.passed ? 'Passed: ' : 'Failed: '}</span>
                    {rules[i]?.[j] ? describeRule(rules[i][j]) : `Check ${j + 1}`}
                    {!c.passed && c.message && <span className="hint">: {c.message}</span>}
                  </li>
                ))}
              </ul>
            )}
            <ResponseBody response={resp} label={label} hint="Click a field to check it or save its value for later steps."
              picked={picked?.step === i ? picked.row : null} onPick={(row) => setPicked(row ? { step: i, row } : null)}
              actions={(row) => {
                if (!step) return <small className="hint">This step was removed after the test.</small>
                const fullChecks = step.assertions.length >= MAX_PER_STEP
                const saved = step.extractions.find((x) => x.from === 'json_path' && x.path === row.path)
                const name = valueNameFor(row.path ?? '', taken)
                const single = row.kind !== 'object' && row.kind !== 'array'
                return (
                  <>
                    {rulesFor(row).map((a, j) => (
                      <button key={j} type="button" className="value-chip" disabled={fullChecks}
                        onClick={() => {
                          update(step.key, (s) => ({ assertions: [...s.assertions, a], optionsOpen: true }))
                          setDone(`Added to step ${steps.indexOf(step) + 1}: ${describeRule(a)}.`)
                          setPicked(null)
                        }}>
                        {describeRule(a).slice((row.path ?? '').length + 1)}
                      </button>
                    ))}
                    {single && (saved ? (
                      <small className="hint">Saved as {`{{${saved.name}}}`}</small>
                    ) : (
                      <button type="button" className="value-chip" disabled={step.extractions.length >= MAX_PER_STEP}
                        onClick={() => {
                          update(step.key, (s) => ({ extractions: [...s.extractions, { name, from: 'json_path', path: row.path ?? '' }] }))
                          setDone(`Step ${steps.indexOf(step) + 1} saves ${row.path} as {{${name}}}. Later steps can use it.`)
                          setPicked(null)
                        }}>
                        Save as {`{{${name}}}`}
                      </button>
                    ))}
                    {fullChecks && <small className="hint">This step has as many checks as it can have.</small>}
                  </>
                )
              }} />
          </details>
        )
      })}
      {done && <p role="status" className="hint">{done}</p>}
    </div>
  )
}
