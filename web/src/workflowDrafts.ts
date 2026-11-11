import { BODY_METHODS, WRITE_METHODS, type Assertion, type AuthType, type Extraction, type HttpMethod, type RequestHeader, type WorkflowInfo, type WorkflowStepInput } from './api'

/** The multi-step API check editor's working copy of a workflow (components/WorkflowEditor.tsx), and its conversion to and from the API's form. */

export interface StepDraft {
  key: number
  name: string
  method: HttpMethod
  url: string
  headers: RequestHeader[]
  body: string
  authType: AuthType
  authUsername: string
  authHeaderName: string
  authSecret: string
  /** The auth type whose secret is already stored for this step; leaving the secret empty keeps it. */
  storedAuthType: AuthType | null
  expected: string
  assertions: Assertion[]
  extractions: Extraction[]
  timeout: string
  optionsOpen: boolean
}

export interface SecretDraft {
  key: number
  name: string
  value: string
  /** Already saved (its value is never shown); an empty value keeps it. */
  stored: boolean
  /** Named but not entered yet (an assistant created the check through MCP, which never sees secret values). */
  pending: boolean
  removed: boolean
}

let nextKey = 1
/** A fresh React key for a step or secret row. */
export const key = () => nextKey++

export function newStep(n: number): StepDraft {
  return {
    key: key(), name: n === 1 ? 'Sign in' : `Step ${n}`, method: 'GET', url: 'https://', headers: [], body: '', authType: 'none', authUsername: '',
    authHeaderName: '', authSecret: '', storedAuthType: null, expected: '', assertions: [], extractions: [], timeout: '10', optionsOpen: false,
  }
}

/** Drafts from a saved workflow. */
export function draftsFrom(w: WorkflowInfo): { steps: StepDraft[]; secrets: SecretDraft[] } {
  return {
    steps: w.steps.map((s) => ({
      key: key(), name: s.name, method: s.method, url: s.url, headers: s.headers ?? [], body: s.body ?? '', authType: s.auth.type,
      authUsername: s.auth.username ?? '', authHeaderName: s.auth.header_name ?? '', authSecret: '', storedAuthType: s.auth.secret_set ? s.auth.type : null,
      expected: s.expected_status_code?.toString() ?? '', assertions: s.assertions ?? [], extractions: s.extractions ?? [],
      timeout: String(s.timeout_seconds), optionsOpen: false,
    })),
    secrets: w.secrets.map((s) => ({ key: key(), name: s.name, value: '', stored: s.set, pending: !s.set, removed: false })),
  }
}

/** The steps as the API takes them. A step's stored secret is kept by leaving secret out. */
export function stepsInput(steps: StepDraft[]): WorkflowStepInput[] {
  return steps.map((s) => {
    const auth: WorkflowStepInput['auth'] = { type: s.authType }
    if (s.authType === 'basic') auth.username = s.authUsername
    if (s.authType === 'header') auth.header_name = s.authHeaderName
    if (s.authType !== 'none' && s.authSecret !== '') auth.secret = s.authSecret
    return {
      name: s.name, method: s.method, url: s.url, headers: s.headers.filter((h) => h.name.trim() !== '' || h.value !== ''),
      body: BODY_METHODS.includes(s.method) && s.body !== '' ? s.body : null, auth,
      expected_status_code: s.expected === '' ? null : Number(s.expected), assertions: s.method === 'HEAD' ? [] : s.assertions,
      extractions: s.extractions, timeout_seconds: Number(s.timeout) || 10,
    }
  })
}

/** The secret changes to send: new and changed values, and null for removed ones. Stored secrets left alone are not sent. */
export function secretsInput(secrets: SecretDraft[]): Record<string, string | null> {
  const out: Record<string, string | null> = {}
  for (const s of secrets) {
    const name = s.name.trim()
    if (!name) continue
    if (s.removed) {
      if (s.stored || s.pending) out[name] = null
    } else if (s.value !== '') {
      out[name] = s.value
    }
  }
  return out
}

export const usesWriteMethod = (steps: StepDraft[]) => steps.some((s) => WRITE_METHODS.includes(s.method))
