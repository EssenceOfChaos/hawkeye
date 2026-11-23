import type { Assertion, JsonOperator } from './api'

/** The JSON check operators, in the order the menus show them. Shared by the monitor form and multi-step checks. */
export const JSON_OPERATORS: { value: JsonOperator; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'not_equals', label: 'does not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'greater_than', label: 'is greater than' },
  { value: 'greater_or_equal', label: 'is at least' },
  { value: 'less_than', label: 'is less than' },
  { value: 'less_or_equal', label: 'is at most' },
  { value: 'exists', label: 'exists' },
  { value: 'not_exists', label: 'does not exist' },
]

const NUMBER_OPERATORS: JsonOperator[] = ['greater_than', 'greater_or_equal', 'less_than', 'less_or_equal']

/** Whether the operator compares numbers, so its value must be a number. */
export const isNumberOperator = (op?: JsonOperator) => op !== undefined && NUMBER_OPERATORS.includes(op)

/** Whether a check needs a value typed in: every check except "exists" and "does not exist". */
export const needsValue = (a: Assertion) => a.type !== 'json_path' || (a.operator !== 'exists' && a.operator !== 'not_exists')

/** A check in words, as the test results list it: `data.status equals "ok"`, `queue_size is at most 500`. */
export function describeRule(a: Assertion): string {
  if (a.type === 'body_contains') return `Response contains "${a.value ?? ''}"`
  if (a.type === 'body_not_contains') return `Response does not contain "${a.value ?? ''}"`
  const op = JSON_OPERATORS.find((o) => o.value === a.operator)?.label ?? a.operator ?? ''
  if (!needsValue(a)) return `${a.path} ${op}`
  return `${a.path} ${op} ${isNumberOperator(a.operator) ? (a.value ?? '').trim() : `"${a.value ?? ''}"`}`
}

/**
 * The path to a child of `parent` (null for the document itself), in the form HawkEye's JSON checks read: `data.items[0].status`. Returns null
 * when the key can't be written that way: keys are separated by dots and indexes use brackets, so a key with either can't be reached.
 */
export function childPath(parent: string | null, key: string | number): string | null {
  if (typeof key === 'number') return `${parent ?? ''}[${key}]`
  if (key === '' || /[.[\]]/.test(key)) return null
  return parent ? `${parent}.${key}` : key
}

export type JsonKind = 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null'

/** One line of a JSON response as the test request shows it. */
export interface JsonRow {
  /** The path a check would use, or null when a key on the way can't be written as a path. */
  path: string | null
  depth: number
  /** The key or [index] shown at the start of the line. */
  label: string
  kind: JsonKind
  value: unknown
  /** For objects and arrays: how many fields or items. */
  size?: number
}

export function kindOf(v: unknown): JsonKind {
  if (v === null) return 'null'
  if (Array.isArray(v)) return 'array'
  return typeof v === 'object' ? 'object' : (typeof v as 'string' | 'number' | 'boolean')
}

/**
 * Flattens a parsed JSON document into lines, depth first, stopping after `cap` lines so a large response stays quick to show. The document
 * itself has no line: its fields (or items) are the top level. `more` says whether lines were left out.
 */
export function flattenJson(doc: unknown, cap = 1000): { rows: JsonRow[]; more: boolean } {
  const rows: JsonRow[] = []
  let more = false
  const walk = (v: unknown, path: string | null, reachable: boolean, depth: number) => {
    const entries: [string | number, unknown][] = Array.isArray(v) ? v.map((x, i) => [i, x]) : Object.entries(v as Record<string, unknown>)
    for (const [k, child] of entries) {
      if (rows.length >= cap) {
        more = true
        return
      }
      const p = reachable ? childPath(path, k) : null
      const kind = kindOf(child)
      const label = typeof k === 'number' ? `[${k}]` : k
      const container = kind === 'object' || kind === 'array'
      rows.push({ path: p, depth, label, kind, value: child,
        size: container ? (Array.isArray(child) ? child.length : Object.keys(child as object).length) : undefined })
      if (container) walk(child, p, p !== null, depth + 1)
    }
  }
  const kind = kindOf(doc)
  if (kind === 'object' || kind === 'array') walk(doc, null, true, 0)
  return { rows, more }
}

/** The checks a clicked line offers, most likely first. Objects and arrays can only be checked to exist. */
export function rulesFor(row: JsonRow): Assertion[] {
  if (row.path === null) return []
  const path = row.path
  const rule = (operator: JsonOperator, value?: string): Assertion =>
    value === undefined ? { type: 'json_path', path, operator } : { type: 'json_path', path, operator, value }
  switch (row.kind) {
    case 'string':
      return [rule('equals', row.value as string), rule('exists')]
    case 'number': {
      const n = String(row.value)
      return [rule('equals', n), rule('greater_or_equal', n), rule('less_or_equal', n), rule('exists')]
    }
    case 'boolean':
    case 'null':
      return [rule('equals', String(row.value)), rule('exists')]
    default:
      return [rule('exists')]
  }
}
