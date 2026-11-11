import { useState } from 'react'
import {
  api,
  ApiError,
  BODY_METHODS,
  HTTP_METHODS,
  type Assertion,
  type AuthType,
  type ExtractFrom,
  type Extraction,
  type HttpMethod,
  type JsonOperator,
  type RequestHeader,
  type WorkflowRun as Run,
} from '../api'
import { Alert } from './Alert'
import { key, newStep, stepsInput, type SecretDraft, type StepDraft } from '../workflowDrafts'
import { Field } from './Field'
import { Toggle } from './Toggle'
import { WorkflowRun } from './WorkflowRun'

/**
 * The editor for a multi-step API check: numbered step cards (the same numbers as the run display), each showing only its name, method and address
 * until "More options" is opened; a row per value to save for later steps; the workflow's secrets; and Run test, which tries the workflow as it is
 * on screen without saving it.
 */

const OPERATORS: { value: JsonOperator; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'not_equals', label: 'does not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'exists', label: 'exists' },
  { value: 'not_exists', label: 'does not exist' },
]

const FROM: { value: ExtractFrom; label: string; placeholder: string }[] = [
  { value: 'json_path', label: 'JSON path', placeholder: 'data.token' },
  { value: 'header', label: 'Header', placeholder: 'Location' },
  { value: 'cookie', label: 'Cookie', placeholder: 'session' },
]

/** Copies a placeholder such as {{token}}, so it can be pasted where it is needed. */
function ValueChip({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button type="button" className="value-chip" aria-label={`Copy ${text}`}
      onClick={() => void navigator.clipboard?.writeText(text).then(() => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      })}>
      {copied ? 'Copied' : text}
    </button>
  )
}

interface EditorProps {
  steps: StepDraft[]
  onSteps: (s: StepDraft[]) => void
  secrets: SecretDraft[]
  onSecrets: (s: SecretDraft[]) => void
  cookies: boolean
  onCookies: (v: boolean) => void
  maxSteps: number
  /** Field errors from the server, keyed as it sends them (steps[0].url, secrets.password...). */
  errors: Record<string, string>
}

export function WorkflowEditor({ steps, onSteps, secrets, onSecrets, cookies, onCookies, maxSteps, errors: f }: EditorProps) {
  const [run, setRun] = useState<Run | null>(null)
  const [ranNames, setRanNames] = useState<string[]>([])
  const [testError, setTestError] = useState<ApiError | null>(null)
  const [testing, setTesting] = useState(false)

  const set = (i: number, patch: Partial<StepDraft>) => onSteps(steps.map((s, j) => (j === i ? { ...s, ...patch } : s)))
  const move = (i: number, by: number) => {
    const next = [...steps]
    ;[next[i], next[i + by]] = [next[i + by], next[i]]
    onSteps(next)
  }
  const liveSecrets = secrets.filter((s) => !s.removed && s.name.trim() !== '')
  // A test sends everything it needs: secrets that are saved but not typed in again can't be used.
  const missingForTest = liveSecrets.filter((s) => s.stored && s.value === '').map((s) => s.name)

  async function test() {
    setTesting(true)
    setTestError(null)
    setRun(null)
    try {
      const plain = Object.fromEntries(liveSecrets.filter((s) => s.value !== '').map((s) => [s.name.trim(), s.value]))
      const r = await api<{ result: Run }>('POST', '/monitors/test-workflow', { steps: stepsInput(steps), secrets: plain, cookies })
      setRanNames(steps.map((s) => s.name))
      setRun(r.result)
    } catch (e) {
      setTestError(e instanceof ApiError ? e : new ApiError(0, 'NETWORK', 'Could not reach the server. Try again.'))
    } finally {
      setTesting(false)
    }
  }
  const testFields = testError?.fields ?? {}

  return (
    <>
      <p>
        HawkEye runs these requests in order on every check, and the check fails at the first step that fails. A step can save a value from its
        response, such as a sign-in token, and later steps use it by writing its name in double braces.
      </p>
      {f.steps && <Alert>{f.steps}</Alert>}
      {steps.map((s, i) => (
        <StepCard key={s.key} step={s} index={i} count={steps.length} errors={{ ...f, ...testFields }} onChange={(p) => set(i, p)}
          onMove={(by) => move(i, by)} onRemove={() => onSteps(steps.filter((_, j) => j !== i))}
          values={[...steps.slice(0, i).flatMap((x) => x.extractions.map((e) => e.name.trim())).filter(Boolean).map((n) => `{{${n}}}`),
            ...liveSecrets.map((x) => `{{secret.${x.name.trim()}}}`)]} />
      ))}
      <div className="row">
        <button type="button" className="linklike" disabled={steps.length >= maxSteps} onClick={() => onSteps([...steps, newStep(steps.length + 1)])}>
          Add step
        </button>
        <small className="hint">{steps.length >= maxSteps ? `Your plan allows up to ${maxSteps} steps.` : `Up to ${maxSteps} steps on your plan.`}</small>
      </div>

      <SecretsEditor secrets={secrets} onChange={onSecrets} errors={f} />

      <fieldset className="group">
        <legend>Cookies</legend>
        <Toggle checked={cookies} onToggle={onCookies}>Send cookies from earlier steps</Toggle>
        <p className="hint">For sites that sign in with a session cookie. Cookies last for one run and are never stored.</p>
      </fieldset>

      <fieldset className="group" aria-labelledby="test-h">
        <legend id="test-h">Try it</legend>
        <p className="hint">Runs the steps once, as they are on screen, without saving. Nothing about the responses is kept.</p>
        {missingForTest.length > 0 && (
          <p className="hint">Saved secrets are never shown, so type {missingForTest.join(', ')} again to use {missingForTest.length === 1 ? 'it' : 'them'} in a test.</p>
        )}
        <div className="row">
          <button type="button" onClick={() => void test()} disabled={testing}>{testing ? 'Running…' : 'Run test'}</button>
        </div>
        {testError && !Object.keys(testFields).length && <Alert>{testError.message}</Alert>}
        {testError && Object.keys(testFields).length > 0 && <Alert>Fix the highlighted fields, then run the test again.</Alert>}
        {run && (
          <div role="status">
            <p className={run.success ? 'ok' : 'error'}>
              {run.success
                ? `All ${run.steps.length} steps passed in ${(run.response_time_ms ?? 0).toLocaleString()} ms.`
                : run.error_category === 'workflow_config' ? `The test needs fixing: ${run.error_summary}` : `The check would fail: ${run.error_summary}`}
            </p>
            <WorkflowRun names={ranNames} outcomes={run.steps} label="Test run" />
          </div>
        )}
      </fieldset>
    </>
  )
}

interface CardProps {
  step: StepDraft
  index: number
  count: number
  errors: Record<string, string>
  onChange: (p: Partial<StepDraft>) => void
  onMove: (by: number) => void
  onRemove: () => void
  /** Placeholders this step may use: values saved by earlier steps, and secrets. */
  values: string[]
}

function StepCard({ step: s, index: i, count, errors, onChange, onMove, onRemove, values }: CardProps) {
  const err = (k: string) => errors[`steps[${i}].${k}`]
  const prefix = `steps[${i}].`
  const optionError = Object.keys(errors).some((k) => k.startsWith(prefix) && !['name', 'method', 'url', 'extractions'].some((x) => k === prefix + x || k.startsWith(prefix + 'extractions')))
  const n = i + 1
  const label = s.name.trim() || `Step ${n}`
  const bodyAllowed = BODY_METHODS.includes(s.method)
  const keeps = s.storedAuthType === s.authType
  const setHeader = (j: number, p: Partial<RequestHeader>) => onChange({ headers: s.headers.map((h, k) => (k === j ? { ...h, ...p } : h)) })
  const setAssertion = (j: number, p: Partial<Assertion>) => onChange({ assertions: s.assertions.map((a, k) => (k === j ? { ...a, ...p } : a)) })
  const setExtraction = (j: number, p: Partial<Extraction>) => onChange({ extractions: s.extractions.map((x, k) => (k === j ? { ...x, ...p } : x)) })

  return (
    <fieldset className="group workflow-step">
      <legend>
        <span className="wf-no" aria-hidden="true">{n}</span> Step {n}
      </legend>
      <Field label="Name" required maxLength={100} value={s.name} onChange={(e) => onChange({ name: e.target.value })} error={err('name')}
        placeholder="Sign in" />
      <div className="workflow-request">
        <div className="field">
          <label htmlFor={`step-${s.key}-method`}>Method</label>
          <select id={`step-${s.key}-method`} value={s.method} onChange={(e) => onChange({ method: e.target.value as HttpMethod })}
            aria-invalid={err('method') ? true : undefined}>
            {HTTP_METHODS.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </div>
        <Field label="URL" required value={s.url} onChange={(e) => onChange({ url: e.target.value })} error={err('url')}
          placeholder="https://api.example.com/login" />
      </div>
      {values.length > 0 && (
        <div className="workflow-values">
          <span className="hint">Values you can use here (click to copy):</span> {values.map((v) => <ValueChip key={v} text={v} />)}
        </div>
      )}

      <div className="field">
        <span>Save values for later steps</span>
        {s.extractions.map((x, j) => {
          const from = FROM.find((f) => f.value === x.from) ?? FROM[0]
          return (
            <div key={j}>
              <div className="kv-row">
                <select aria-label={`Step ${n}, value ${j + 1}: read from`} value={x.from} onChange={(e) => setExtraction(j, { from: e.target.value as ExtractFrom })}>
                  {FROM.map((f) => <option key={f.value} value={f.value}>{f.label}</option>)}
                </select>
                <input aria-label={`Step ${n}, value ${j + 1}: ${from.label}`} placeholder={from.placeholder} value={x.path}
                  onChange={(e) => setExtraction(j, { path: e.target.value })} />
                <span className="hint">as</span>
                <input aria-label={`Step ${n}, value ${j + 1}: name`} placeholder="token" value={x.name} className="mono"
                  onChange={(e) => setExtraction(j, { name: e.target.value })} />
                <button type="button" className="linklike" aria-label={`Remove value ${j + 1} of step ${n}`}
                  onClick={() => onChange({ extractions: s.extractions.filter((_, k) => k !== j) })}>Remove</button>
              </div>
              {err(`extractions[${j}]`) && <small className="error row-error">{err(`extractions[${j}]`)}</small>}
            </div>
          )
        })}
        {err('extractions') && <small className="error row-error">{err('extractions')}</small>}
        <div>
          <button type="button" className="linklike" onClick={() => onChange({ extractions: [...s.extractions, { name: '', from: 'json_path', path: '' }] })}>
            Save a value
          </button>
        </div>
        {s.extractions.length === 0 && <small className="hint">For example, save the token from a sign-in response as token, and send {'{{token}}'} in the next step.</small>}
      </div>

      <details open={s.optionsOpen || optionError} onToggle={(e) => onChange({ optionsOpen: (e.target as HTMLDetailsElement).open })}>
        <summary>More options: headers, body, authentication and response checks</summary>
        <div className="field">
          <span>Headers</span>
          {s.headers.map((h, j) => (
            <div key={j}>
              <div className="kv-row">
                <input aria-label={`Step ${n}, header ${j + 1} name`} placeholder="X-Header-Name" value={h.name} onChange={(e) => setHeader(j, { name: e.target.value })} />
                <input aria-label={`Step ${n}, header ${j + 1} value`} placeholder="value" value={h.value} onChange={(e) => setHeader(j, { value: e.target.value })} />
                <button type="button" className="linklike" aria-label={`Remove header ${j + 1} of step ${n}`}
                  onClick={() => onChange({ headers: s.headers.filter((_, k) => k !== j) })}>Remove</button>
              </div>
              {(err(`headers[${j}].name`) || err(`headers[${j}].value`)) && <small className="error row-error">{err(`headers[${j}].name`) ?? err(`headers[${j}].value`)}</small>}
            </div>
          ))}
          <div>
            <button type="button" className="linklike" onClick={() => onChange({ headers: [...s.headers, { name: '', value: '' }] })}>Add header</button>
          </div>
        </div>
        {bodyAllowed && (
          <div className="field">
            <label htmlFor={`step-${s.key}-body`}>Request body (optional)</label>
            <textarea id={`step-${s.key}-body`} rows={3} value={s.body} onChange={(e) => onChange({ body: e.target.value })}
              placeholder={'{"email": "monitor@example.com", "password": "{{secret.password}}"}'} aria-invalid={err('body') ? true : undefined} />
            <small className="hint">In a JSON body, put values inside quotes: "{'{{token}}'}".</small>
            {err('body') && <small className="error">{err('body')}</small>}
          </div>
        )}
        <div className="field">
          <label htmlFor={`step-${s.key}-auth`}>Authentication</label>
          <select id={`step-${s.key}-auth`} value={s.authType} onChange={(e) => onChange({ authType: e.target.value as AuthType })}>
            <option value="none">None</option>
            <option value="basic">Basic (username and password)</option>
            <option value="bearer">Bearer token</option>
            <option value="header">API key header</option>
          </select>
        </div>
        {s.authType === 'basic' && <Field label="Username" value={s.authUsername} onChange={(e) => onChange({ authUsername: e.target.value })} error={err('auth.username')} />}
        {s.authType === 'header' && (
          <Field label="Header name" placeholder="X-API-Key" value={s.authHeaderName} onChange={(e) => onChange({ authHeaderName: e.target.value })} error={err('auth.header_name')} />
        )}
        {s.authType !== 'none' && (
          <Field label={s.authType === 'basic' ? 'Password' : s.authType === 'bearer' ? 'Token' : 'Header value'} type={keeps ? 'password' : 'text'}
            autoComplete="off" value={s.authSecret} onChange={(e) => onChange({ authSecret: e.target.value })} error={err('auth.secret')}
            placeholder={keeps ? '••••••••' : '{{token}}'}
            hint={keeps ? 'Saved and hidden. Leave empty to keep it.' : 'A value from an earlier step, such as {{token}}. For a fixed key, add it under Secrets and write {{secret.name}} here, so it stays hidden.'} />
        )}
        <Field label="Expected status code (optional)" type="number" min={100} max={599} value={s.expected} onChange={(e) => onChange({ expected: e.target.value })}
          error={err('expected_status_code')} hint="Leave empty to accept any status below 400." />
        {s.method !== 'HEAD' && (
          <div className="field">
            <span>Response checks</span>
            {s.assertions.map((a, j) => (
              <div key={j}>
                <div className="kv-row">
                  <select aria-label={`Step ${n}, check ${j + 1}: type`} value={a.type}
                    onChange={(e) => setAssertion(j, { type: e.target.value as Assertion['type'], path: '', operator: 'equals' })}>
                    <option value="json_path">JSON path</option>
                    <option value="body_contains">Response contains</option>
                    <option value="body_not_contains">Response does not contain</option>
                  </select>
                  {a.type === 'json_path' && (
                    <>
                      <input aria-label={`Step ${n}, check ${j + 1}: path`} placeholder="data.status" value={a.path ?? ''} onChange={(e) => setAssertion(j, { path: e.target.value })} />
                      <select aria-label={`Step ${n}, check ${j + 1}: operator`} value={a.operator ?? 'equals'} onChange={(e) => setAssertion(j, { operator: e.target.value as JsonOperator })}>
                        {OPERATORS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                      </select>
                    </>
                  )}
                  {(a.type !== 'json_path' || (a.operator !== 'exists' && a.operator !== 'not_exists')) && (
                    <input aria-label={`Step ${n}, check ${j + 1}: value`} placeholder={a.type === 'json_path' ? 'active' : 'Welcome'} value={a.value ?? ''}
                      onChange={(e) => setAssertion(j, { value: e.target.value })} />
                  )}
                  <button type="button" className="linklike" aria-label={`Remove check ${j + 1} of step ${n}`}
                    onClick={() => onChange({ assertions: s.assertions.filter((_, k) => k !== j) })}>Remove</button>
                </div>
                {(err(`assertions[${j}].path`) ?? err(`assertions[${j}].value`) ?? err(`assertions[${j}].operator`)) && (
                  <small className="error row-error">{err(`assertions[${j}].path`) ?? err(`assertions[${j}].value`) ?? err(`assertions[${j}].operator`)}</small>
                )}
              </div>
            ))}
            <div>
              <button type="button" className="linklike" disabled={s.assertions.length >= 10}
                onClick={() => onChange({ assertions: [...s.assertions, { type: 'json_path', path: '', operator: 'equals', value: '' }] })}>Add check</button>
            </div>
          </div>
        )}
        <Field label="Timeout (seconds)" type="number" min={1} max={30} value={s.timeout} onChange={(e) => onChange({ timeout: e.target.value })} error={err('timeout_seconds')} />
      </details>

      <div className="row workflow-step-actions">
        <button type="button" className="linklike" disabled={i === 0} aria-label={`Move ${label} up`} onClick={() => onMove(-1)}>Move up</button>
        <button type="button" className="linklike" disabled={i === count - 1} aria-label={`Move ${label} down`} onClick={() => onMove(1)}>Move down</button>
        <button type="button" className="linklike danger" disabled={count === 1} aria-label={`Remove ${label}`} onClick={onRemove}>Remove step</button>
      </div>
    </fieldset>
  )
}

function SecretsEditor({ secrets, onChange, errors: f }: { secrets: SecretDraft[]; onChange: (s: SecretDraft[]) => void; errors: Record<string, string> }) {
  const set = (i: number, p: Partial<SecretDraft>) => onChange(secrets.map((s, j) => (j === i ? { ...s, ...p } : s)))
  const shown = secrets.map((s, i) => ({ s, i })).filter(({ s }) => !s.removed)
  return (
    <fieldset className="group workflow-secrets">
      <legend>Secrets</legend>
      <p className="hint">
        Passwords and keys the steps use, written as {'{{secret.name}}'}. They are stored encrypted, never shown again, and hidden in every result and alert.
        Use a test account made for monitoring, not a real person’s.
      </p>
      {shown.map(({ s, i }) => (
        <div key={s.key}>
          <div className="kv-row">
            <input aria-label={`Secret ${i + 1} name`} placeholder="password" className="mono" value={s.name} disabled={s.stored || s.pending}
              onChange={(e) => set(i, { name: e.target.value })} />
            <input aria-label={`Secret ${s.name || i + 1} value`} type="password" autoComplete="new-password" value={s.value}
              placeholder={s.stored ? 'Saved: leave empty to keep it' : s.pending ? 'Not entered yet' : 'value'}
              aria-invalid={s.pending && s.value === '' ? true : undefined} onChange={(e) => set(i, { value: e.target.value })} />
            <button type="button" className="linklike" aria-label={`Remove secret ${s.name || i + 1}`} onClick={() => set(i, { removed: true })}>Remove</button>
          </div>
          {f[`secrets.${s.name}`] && <small className="error row-error">{f[`secrets.${s.name}`]}</small>}
        </div>
      ))}
      {f.secrets && <small className="error row-error">{f.secrets}</small>}
      {shown.some(({ s }) => s.pending && s.value === '') && (
        <small className="hint">Secrets marked “Not entered yet” were named by an assistant, which never sees their values. Enter them, save, then resume the check.</small>
      )}
      <div>
        <button type="button" className="linklike" onClick={() => onChange([...secrets, { key: key(), name: '', value: '', stored: false, pending: false, removed: false }])}>
          Add secret
        </button>
      </div>
    </fieldset>
  )
}
