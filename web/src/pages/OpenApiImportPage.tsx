import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type TagSummary, type Usage } from '../api'
import { trackEvent } from '../analytics'
import { brand } from '../brand'
import { Alert } from '../components/Alert'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'
import { privateHostReason } from '../curl'
import { MAX_SPEC_BYTES, monitorFor, needsBase, paramKey, ready, type EnteredCredential, type Operation, type SpecSummary } from '../openapi'
import { readSpecInBackground } from '../openapiRead'

/** How many checks are ticked when a spec is read; the person can tick more, up to the plan's room. */
const PRESELECT = 10
/** Rows shown at once; a filter narrows larger specs. */
const MAX_ROWS = 300

interface Outcome {
  id?: string
  error?: string
  upgrade?: boolean
}

function describeChecks(op: Operation): string {
  const status = op.expectedStatus ? `status ${op.expectedStatus}` : 'any status below 400'
  if (op.assertions.length === 0) return `Expects ${status}.`
  const fields = op.assertions.map((a) => (a.operator === 'equals' ? `${a.path} is ${a.value}` : a.path))
  return `Expects ${status}, with ${fields.join(', ')} in the response.`
}

function errorText(e: unknown): Outcome {
  if (!(e instanceof ApiError)) return { error: 'Could not add this check. Try again.' }
  const fields = Object.entries(e.fields ?? {}).map(([k, v]) => `${k} ${v}`)
  return { error: fields.length > 0 ? fields.join('; ') : e.message, upgrade: e.code === 'PLAN_LIMIT_REACHED' }
}

/**
 * "Import from OpenAPI or Postman": the person uploads or pastes a spec or collection, HawkEye lists the API checks it describes with sensible ones ticked, and creates the
 * chosen ones through POST /monitors (so plan limits and validation are those of adding one by hand). The spec is read in the browser and never
 * sent; only the checks are. See docs/plan-api-import.md.
 */
export function OpenApiImportPage() {
  const [text, setText] = useState('')
  const [fileName, setFileName] = useState('')
  const [reading, setReading] = useState(false)
  const [readError, setReadError] = useState<string | null>(null)
  const [spec, setSpec] = useState<SpecSummary | null>(null)
  const [base, setBase] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [values, setValues] = useState<Record<string, string>>({})
  const [bodies, setBodies] = useState<Record<string, string>>({})
  const [entered, setEntered] = useState<Record<string, EnteredCredential>>({})
  const [tagOn, setTagOn] = useState(true)
  const [filter, setFilter] = useState('')
  const [usage, setUsage] = useState<Usage | null>(null)
  const [busy, setBusy] = useState(false)
  const [outcomes, setOutcomes] = useState<Record<string, Outcome>>({})
  const [tagNote, setTagNote] = useState<string | null>(null)

  useEffect(() => {
    api<{ usage: Usage }>('GET', '/usage').then((r) => setUsage(r.usage), () => setUsage(null))
  }, [])
  const room = usage ? Math.max(0, usage.monitor_limit - usage.monitors_used) : Infinity

  async function read(source: string) {
    setReading(true)
    setReadError(null)
    const r = await readSpecInBackground(source)
    setReading(false)
    if (!r.ok) {
      setReadError(r.error)
      return
    }
    const s = r.spec
    setSpec(s)
    setOutcomes({})
    setValues({})
    setBodies(Object.fromEntries(s.operations.map((o) => [o.id, o.body])))
    setEntered(Object.fromEntries(s.credentials.filter((c) => c.prefill).map((c) => [c.key, c.prefill!])))
    setFilter('')
    setBase(s.servers.find((u) => !needsBase(u)) ?? '')
    const preselect = s.operations
      .filter((o) => !o.write && !o.deprecated && ready(o, {}, o.body, s.credentials))
      .slice(0, Math.min(PRESELECT, room))
      .map((o) => o.id)
    setPicked(preselect)
    trackEvent('import_openapi_read', { format: s.format, operations: String(s.operations.length), preselected: String(preselect.length) })
  }

  async function onFile(file: File | undefined) {
    if (!file) return
    setFileName(file.name)
    if (file.size > MAX_SPEC_BYTES) {
      setReadError('The spec is larger than 5 MB. Remove the parts you don’t need to monitor, or split it.')
      return
    }
    const content = await file.text()
    setText('')
    await read(content)
  }

  const ops = useMemo(() => spec?.operations ?? [], [spec])
  const chosen = ops.filter((o) => picked.includes(o.id) && !outcomes[o.id]?.id)
  const neededCredentials = (spec?.credentials ?? []).filter((c) => c.kind !== 'unsupported' && chosen.some((o) => o.credential === c.key))
  const missingCredentials = neededCredentials.filter((c) => (entered[c.key]?.secret ?? '') === '' || (c.kind === 'basic' && (entered[c.key]?.username ?? '') === ''))
  const notReady = chosen.filter((o) => !ready(o, values, bodies[o.id] ?? '', spec?.credentials ?? []))
  // A Postman collection whose requests go to several hosts keeps each request's full address, so there's no base address to ask for.
  const allAbsolute = ops.length > 0 && ops.every((o) => o.absolute)
  let baseProblem: string | null = null
  if (spec && !allAbsolute) {
    if (base.trim() === '' || needsBase(base)) baseProblem = 'Enter the full public address of the API, starting with https://.'
    else {
      try {
        const reason = privateHostReason(new URL(base))
        if (reason) baseProblem = `${reason} HawkEye checks from the internet, so use the public address.`
      } catch {
        baseProblem = 'That isn’t a valid address.'
      }
    }
  }
  const tagName = (spec?.title ?? '').slice(0, 40)
  const canCreate = chosen.length > 0 && !busy && !baseProblem && missingCredentials.length === 0 && notReady.length === 0

  async function tagID(): Promise<string[]> {
    if (!tagOn || tagName === '') return []
    try {
      const { tags } = await api<{ tags: TagSummary[] }>('GET', '/tags')
      const existing = (tags ?? []).find((t) => t.name.toLowerCase() === tagName.toLowerCase())
      if (existing) return [existing.id]
      const r = await api<{ tag: TagSummary }>('POST', '/tags', { name: tagName })
      return [r.tag.id]
    } catch (e) {
      setTagNote(`The checks were added without the “${tagName}” tag: ${e instanceof ApiError ? e.message : 'it could not be created.'}`)
      return []
    }
  }

  async function create() {
    if (!spec) return
    setBusy(true)
    setTagNote(null)
    const tags = await tagID()
    const next = { ...outcomes }
    // One at a time, in the order shown, so a plan limit stops at the same place every time.
    for (const op of chosen) {
      try {
        const r = await api<{ monitor: { id: string } }>('POST', '/monitors',
          monitorFor(op, base.trim(), values, bodies[op.id] ?? '', spec.credentials, entered, tags))
        next[op.id] = { id: r.monitor.id }
      } catch (e) {
        next[op.id] = errorText(e)
      }
      setOutcomes({ ...next })
    }
    const created = chosen.filter((o) => next[o.id]?.id)
    setPicked((p) => p.filter((id) => !next[id]?.id))
    trackEvent('import_openapi', { format: spec.format, offered: String(spec.operations.length), created: String(created.length),
      failed: String(chosen.length - created.length) })
    setBusy(false)
    api<{ usage: Usage }>('GET', '/usage').then((r) => setUsage(r.usage), () => {})
  }

  const toggle = (id: string, on: boolean) => setPicked((p) => (on ? [...p, id] : p.filter((x) => x !== id)))
  const q = filter.trim().toLowerCase()
  const shown = q ? ops.filter((o) => `${o.method} ${o.path} ${o.name} ${o.group}`.toLowerCase().includes(q)) : ops
  const groups = new Map<string, Operation[]>()
  for (const o of shown.slice(0, MAX_ROWS)) groups.set(o.group, [...(groups.get(o.group) ?? []), o])
  const added = Object.values(outcomes).filter((o) => o.id).length
  const failed = Object.values(outcomes).filter((o) => o.error).length

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.monitors} />
        <h1>Import from OpenAPI or Postman</h1>
      </div>
      <p>
        Give HawkEye your API’s OpenAPI (3.x) or Swagger (2.0) file, or a Postman collection, and it lists the checks it describes, with the useful ones
        ticked: health endpoints first, then endpoints that read data. Each check tests the status code, and for OpenAPI that the fields your spec marks as
        required are in the response. The file is read here in your browser; only the checks you add are sent to HawkEye.
      </p>

      <section className="card narrow-left" aria-labelledby="spec-h">
        <h2 id="spec-h" className="sr-only">Your spec</h2>
        <div className="field">
          <label htmlFor="spec-file">OpenAPI, Swagger or Postman file (JSON or YAML)</label>
          <input id="spec-file" type="file" accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml"
            onChange={(e) => void onFile(e.target.files?.[0])} />
          {fileName && <small className="hint">{fileName}</small>}
        </div>
        <div className="field">
          <label htmlFor="spec-text">Or paste it</label>
          <textarea id="spec-text" rows={6} className="mono" spellCheck={false} value={text} placeholder={'openapi: 3.1.0\ninfo:\n  title: Orders API\n…'}
            onChange={(e) => {
              setText(e.target.value)
              setReadError(null)
            }} />
        </div>
        {readError && <Alert>{readError}</Alert>}
        <button type="button" disabled={reading || text.trim() === ''} onClick={() => void read(text)}>
          {reading ? <><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Reading…</> : 'Read the file'}
        </button>
      </section>

      {spec && (
        <section className="card" aria-labelledby="ops-h">
          <h2 id="ops-h">{spec.title}</h2>
          <p className="hint">
            {spec.format}, {ops.length} operation{ops.length === 1 ? '' : 's'}.
            {usage && ` Your plan has room for ${room} more monitor${room === 1 ? '' : 's'}.`}
          </p>
          {spec.notes.map((n) => <p key={n} className="hint">{n}</p>)}

          {!allAbsolute && <><div className="field">
            {spec.servers.filter((u) => !needsBase(u)).length > 1 && (
              <>
                <label htmlFor="oa-server">Server</label>
                <select id="oa-server" value={spec.servers.includes(base) ? base : ''} onChange={(e) => setBase(e.target.value)}>
                  {spec.servers.filter((u) => !needsBase(u)).map((u) => <option key={u} value={u}>{u}</option>)}
                  <option value="">Another address…</option>
                </select>
              </>
            )}
          </div>
          <Field label="API address" value={base} onChange={(e) => setBase(e.target.value)} error={base === '' ? undefined : baseProblem ?? undefined}
            placeholder="https://api.example.com/v1"
            hint={spec.servers.some(needsBase) && !spec.servers.some((u) => !needsBase(u))
              ? `The file gives ${spec.servers.join(', ')}, which isn’t a full address${spec.servers.some((u) => u.includes('{{')) ? ' (in Postman its value usually lives in an environment)' : ''}. Type the public address your API answers on.`
              : 'Paths from the file are added to this. Change it to check another environment.'} /></>}

          {neededCredentials.length > 0 && (
            <fieldset className="group">
              <legend>Credentials</legend>
              <p className="hint">
                Entered once and stored encrypted on each check that uses them. They’re never shown again.
                {neededCredentials.some((c) => c.prefill) && ' Values the file already held are filled in; change them if they belong to a test account you don’t want monitored.'}
              </p>
              {neededCredentials.map((c) => (
                <div key={c.key}>
                  {c.kind === 'basic' && (
                    <Field label={`Username (${c.key})`} value={entered[c.key]?.username ?? ''} autoComplete="off"
                      onChange={(e) => setEntered((x) => ({ ...x, [c.key]: { ...x[c.key], secret: x[c.key]?.secret ?? '', username: e.target.value } }))} />
                  )}
                  <Field label={`${c.kind === 'basic' ? 'Password' : c.label} (${c.key})`} type="password" autoComplete="new-password"
                    value={entered[c.key]?.secret ?? ''}
                    onChange={(e) => setEntered((x) => ({ ...x, [c.key]: { ...x[c.key], secret: e.target.value } }))} />
                </div>
              ))}
            </fieldset>
          )}

          {ops.length > 10 && (
            <Field label="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="orders, /health, GET…" />
          )}
          {shown.length > MAX_ROWS && <p className="hint">Showing the first {MAX_ROWS} of {shown.length}. Filter to find others.</p>}

          {[...groups.entries()].map(([group, list]) => (
            <fieldset key={group} className="group oa-group">
              <legend>{group}</legend>
              {list.map((op) => {
                const on = picked.includes(op.id)
                const done = outcomes[op.id]?.id
                const cred = op.credential ? spec.credentials.find((c) => c.key === op.credential) : undefined
                const unsupported = cred?.kind === 'unsupported' || Boolean(op.blocked)
                const full = !on && picked.length >= room
                const missing = op.params.filter((p) => (values[paramKey(op, p)] ?? p.value).trim() === '')
                const checkboxID = `op-${op.id.replace(/[^A-Za-z0-9]+/g, '-')}`
                return (
                  <div key={op.id} className={`oa-op${on ? ' on' : ''}`}>
                    <label htmlFor={checkboxID}>
                      <input id={checkboxID} type="checkbox" checked={on || Boolean(done)} disabled={Boolean(done) || unsupported || full}
                        onChange={(e) => toggle(op.id, e.target.checked)} />
                      <span className={`oa-method oa-${op.method.toLowerCase()}`}>{op.method}</span> <code>{op.path}</code>{' '}
                      <span className="oa-name">{op.name}</span>
                    </label>
                    <small className="hint">{describeChecks(op)}</small>
                    {op.write && <small className="hint oa-flag">Changes data: it would run on every check, at most every 5 minutes. Only tick it for an endpoint that’s safe to call again and again.</small>}
                    {op.deprecated && <small className="hint oa-flag">Marked deprecated in the spec.</small>}
                    {op.blocked && <small className="hint oa-flag">{op.blocked}</small>}
                    {!op.blocked && cred?.kind === 'unsupported' && <small className="hint oa-flag">{cred.label}: {cred.why}</small>}
                    {op.hint && on && !done && <small className="hint oa-flag">{op.hint}</small>}
                    {!on && !done && missing.length > 0 && <small className="hint">Needs a value for {missing.map((p) => p.name).join(', ')}.</small>}
                    {!on && !done && op.needsBody && <small className="hint">Needs a request body.</small>}
                    {on && !done && op.params.length > 0 && (
                      <div className="oa-params">
                        {op.params.map((p) => (
                          <Field key={paramKey(op, p)} label={`${p.name} (${p.in === 'path' ? 'in the path' : p.in === 'query' ? 'query' : 'header'})`}
                            value={values[paramKey(op, p)] ?? p.value} required
                            hint={p.description ? p.description.slice(0, 160) : p.in === 'path' ? 'A real value, such as an ID that exists.' : undefined}
                            onChange={(e) => setValues((v) => ({ ...v, [paramKey(op, p)]: e.target.value }))} />
                        ))}
                      </div>
                    )}
                    {on && !done && op.write && (
                      <div className="field">
                        <label htmlFor={`${checkboxID}-body`}>Request body{op.needsBody ? '' : ' (optional)'}</label>
                        <textarea id={`${checkboxID}-body`} rows={3} className="mono" value={bodies[op.id] ?? ''}
                          onChange={(e) => setBodies((b) => ({ ...b, [op.id]: e.target.value }))} />
                      </div>
                    )}
                    {done && <small className="ok">Added. <Link to={`/monitors/${done}`}>Open</Link></small>}
                    {outcomes[op.id]?.error && (
                      <small className="error">{outcomes[op.id].error} {outcomes[op.id].upgrade && <Link to="/billing">See plans</Link>}</small>
                    )}
                  </div>
                )
              })}
            </fieldset>
          ))}

          <label className="oa-tag">
            <input type="checkbox" checked={tagOn} onChange={(e) => setTagOn(e.target.checked)} /> Tag them “{tagName}”, so you can filter, pause or
            remove them together
          </label>
          {notReady.length > 0 && <p className="hint" role="status">Fill in the values and bodies the ticked checks need ({notReady.length} to go).</p>}
          {missingCredentials.length > 0 && <p className="hint" role="status">Enter the credentials the ticked checks use.</p>}
          {baseProblem && base === '' && <p className="hint" role="status">{baseProblem}</p>}
          <button type="button" className="primary" disabled={!canCreate} onClick={() => void create()}>
            {busy ? 'Adding…' : `Add ${chosen.length} check${chosen.length === 1 ? '' : 's'}`}
          </button>
          <p className="hint">Each is checked at your plan’s fastest interval (every 5 minutes for ones that change data). You can change any of them afterward.</p>
          {tagNote && <Alert tone="warning">{tagNote}</Alert>}
          {(added > 0 || failed > 0) && !busy && (
            <Alert tone={failed > 0 ? 'warning' : 'success'}>
              {added > 0 && `Added ${added} check${added === 1 ? '' : 's'}; HawkEye is running their first checks now. `}
              {failed > 0 && `${failed} couldn’t be added; the reason is under each. Fix it and add again.`} <Link to="/monitors">Go to your monitors</Link>
            </Alert>
          )}
        </section>
      )}
    </>
  )
}
