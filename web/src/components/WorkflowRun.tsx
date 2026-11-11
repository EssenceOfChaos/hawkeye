import type { StepOutcome } from '../api'

/**
 * A run of a multi-step API check, drawn with daisyUI's steps: one numbered step per request, green with ✓ when it passed, red with ✕ where the run
 * stopped, and gray for the steps after it, which did not run. Vertical, so a step's name, time and failure reason sit beside it at any width.
 *
 * `names` are the workflow's steps in order; `outcomes` are the steps that ran (a run stops at the first failure).
 */
export function WorkflowRun({ names, outcomes, label }: { names: string[]; outcomes: StepOutcome[]; label: string }) {
  return (
    <ol className="d-steps d-steps-vertical workflow-run" aria-label={label}>
      {names.map((name, i) => {
        const o = outcomes[i]
        const state = !o ? 'skipped' : o.success ? 'passed' : 'failed'
        const cls = state === 'passed' ? 'd-step d-step-success' : state === 'failed' ? 'd-step d-step-error' : 'd-step'
        const words = state === 'passed' ? 'passed' : state === 'failed' ? 'failed' : 'not run'
        return (
          <li key={i} className={cls} data-content={state === 'passed' ? '✓' : state === 'failed' ? '✕' : String(i + 1)}>
            <span className="workflow-run-step">
              <span>
                <strong>{o?.name ?? name}</strong>
                <span className="sr-only">: {words}</span>
              </span>
              <span className="hint">
                {state === 'skipped' && 'Not run'}
                {o && [o.status_code != null && `HTTP ${o.status_code}`, o.response_time_ms != null && `${o.response_time_ms.toLocaleString()} ms`].filter(Boolean).join(' · ')}
              </span>
              {state === 'failed' && o?.error_summary && <span className="error">{o.error_summary}</span>}
            </span>
          </li>
        )
      })}
    </ol>
  )
}
