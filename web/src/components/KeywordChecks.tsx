/**
 * Keyword monitoring without assertions: "the page must contain" and "the page must not contain" lists of plain text. Each phrase is saved as a
 * body_contains or body_not_contains assertion, which the checker matches exactly (capital letters included) against the page's HTML source.
 */
import { PROBLEM_PRESETS } from '../keywords'

interface ListProps {
  id: string
  title: string
  hint: string
  values: string[]
  onChange: (v: string[]) => void
  disabled: boolean
  full: boolean
  errors: Record<number, string | undefined>
  placeholder: string
}

function PhraseList({ id, title, hint, values, onChange, disabled, full, errors, placeholder }: ListProps) {
  return (
    <div className="field">
      <span id={`${id}-label`}>{title}</span>
      {values.map((v, i) => (
        <div key={i}>
          <div className="kv-row">
            <input aria-label={`${title}: text ${i + 1}`} value={v} placeholder={placeholder} disabled={disabled} maxLength={1000}
              aria-invalid={errors[i] ? true : undefined} onChange={(e) => onChange(values.map((x, j) => (j === i ? e.target.value : x)))} />
            <button type="button" className="linklike" aria-label={`Remove “${v || `text ${i + 1}`}” from “${title}”`} disabled={disabled}
              onClick={() => onChange(values.filter((_, j) => j !== i))}>Remove</button>
          </div>
          {errors[i] && <small className="error row-error">{errors[i]}</small>}
        </div>
      ))}
      <div>
        <button type="button" className="linklike" disabled={disabled || full} onClick={() => onChange([...values, ''])}>Add text</button>
      </div>
      <small className="hint">{hint}</small>
    </div>
  )
}

export function KeywordChecks({ contains, notContains, onContains, onNotContains, disabled, full, errors }: {
  contains: string[]
  notContains: string[]
  onContains: (v: string[]) => void
  onNotContains: (v: string[]) => void
  /** HEAD requests have no page to read. */
  disabled: boolean
  /** No room for another check (the limit is shared with JSON checks). */
  full: boolean
  /** Field errors by list and row. */
  errors: { contains: Record<number, string | undefined>; notContains: Record<number, string | undefined> }
}) {
  return (
    <fieldset className="group">
      <legend>Page content</legend>
      <p className="hint">
        Catch a page that loads but is broken. On every check HawkEye reads the page’s HTML (the first 1 MB) and looks for your text exactly as written,
        capital letters included. Text that only appears after the page’s JavaScript runs is not seen.
      </p>
      {disabled ? (
        <p className="hint">HEAD requests have no page content. Choose GET to check the text on the page.</p>
      ) : (
        <>
          <PhraseList id="kw-contains" title="The page must contain" values={contains} onChange={onContains} disabled={disabled} full={full}
            errors={errors.contains} placeholder="Add to cart"
            hint="Text that only appears when the page works: your company name in the footer, “Add to cart” on a product page, or the words on your sign-in button." />
          <PhraseList id="kw-not-contains" title="The page must not contain" values={notContains} onChange={onNotContains} disabled={disabled} full={full}
            errors={errors.notContains} placeholder="Error establishing a database connection"
            hint="Text that means something is wrong. If it appears, the check fails and you are alerted as for an outage." />
          <div className="field">
            <span id="kw-presets">Common problems to watch for</span>
            <ul className="presets" aria-labelledby="kw-presets">
              {PROBLEM_PRESETS.map((p) => {
                const added = notContains.includes(p.text)
                return (
                  <li key={p.text}>
                    <button type="button" disabled={added || full} onClick={() => onNotContains([...notContains.filter((v) => v.trim() !== ''), p.text])}
                      aria-label={added ? `“${p.text}” is already watched for` : `Watch for “${p.text}”: ${p.why}`}>
                      {added ? '✓' : '+'} {p.text}
                    </button>
                    <span className="hint"> {p.why}</span>
                  </li>
                )
              })}
            </ul>
          </div>
        </>
      )}
    </fieldset>
  )
}
