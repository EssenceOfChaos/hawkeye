import { describe, expect, it } from 'vitest'
import { childPath, describeRule, flattenJson, rulesFor } from './jsonRules'

describe('jsonRules', () => {
  it('builds paths the way checks read them', () => {
    expect(childPath(null, 'status')).toBe('status')
    expect(childPath('data', 'items')).toBe('data.items')
    expect(childPath('data.items', 0)).toBe('data.items[0]')
    expect(childPath(null, 2)).toBe('[2]')
    // Dots and brackets separate keys, so a key with one can't be reached.
    expect(childPath('data', 'a.b')).toBeNull()
    expect(childPath('data', 'x[1]')).toBeNull()
    expect(childPath('data', '')).toBeNull()
  })

  it('flattens a response into clickable lines, and keys below an unreachable one stay unreachable', () => {
    const { rows, more } = flattenJson({ database: { status: 'ok' }, 'odd.key': { inner: 1 }, items: [{ n: 3 }] })
    expect(rows.map((r) => [r.path, r.depth, r.kind])).toEqual([
      ['database', 0, 'object'],
      ['database.status', 1, 'string'],
      [null, 0, 'object'],
      [null, 1, 'number'],
      ['items', 0, 'array'],
      ['items[0]', 1, 'object'],
      ['items[0].n', 2, 'number'],
    ])
    expect(rows[0].size).toBe(1)
    expect(more).toBe(false)
    expect(flattenJson({ a: 1, b: 2, c: 3 }, 2)).toMatchObject({ more: true, rows: [{ path: 'a' }, { path: 'b' }] })
    expect(flattenJson('just a string').rows).toEqual([])
  })

  it('offers checks that fit the value', () => {
    const [str] = flattenJson({ status: 'ok' }).rows
    expect(rulesFor(str).map(describeRule)).toEqual(['status equals "ok"', 'status exists'])
    const [num] = flattenJson({ queue_size: 512 }).rows
    expect(rulesFor(num).map(describeRule)).toEqual(['queue_size equals "512"', 'queue_size is at least 512', 'queue_size is at most 512', 'queue_size exists'])
    const [obj] = flattenJson({ db: {} }).rows
    expect(rulesFor(obj)).toEqual([{ type: 'json_path', path: 'db', operator: 'exists' }])
    expect(rulesFor({ path: null, depth: 0, label: 'x', kind: 'string', value: 'y' })).toEqual([])
  })
})
