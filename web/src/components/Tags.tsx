import { useState } from 'react'
import { api, ApiError, MAX_TAG_NAME, MAX_TAGS_PER_MONITOR, type Tag, type TagSummary } from '../api'

/** A tag's name with a dot in its colour. The name always carries the meaning; the colour only helps scanning. */
export function TagChip({ tag }: { tag: Tag }) {
  return (
    <span className="tag">
      <span className="tag-dot" aria-hidden="true" style={tag.color ? { background: tag.color } : undefined} />
      {tag.name}
    </span>
  )
}

export function TagList({ tags }: { tags: Tag[] }) {
  if (tags.length === 0) return null
  return (
    <ul className="tag-list" aria-label="Tags">
      {tags.map((t) => (
        <li key={t.id}><TagChip tag={t} /></li>
      ))}
    </ul>
  )
}

/**
 * Choose a monitor's tags from the organization's tags, or create a new one on the spot (it is selected straight away).
 * onCreated lets the parent add the new tag to its list.
 */
export function TagPicker({ tags, selected, onChange, onCreated, error }: {
  tags: TagSummary[]
  selected: string[]
  onChange: (ids: string[]) => void
  onCreated: (t: TagSummary) => void
  error?: string
}) {
  const [name, setName] = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const full = selected.length >= MAX_TAGS_PER_MONITOR

  async function create() {
    const trimmed = name.trim()
    if (!trimmed) return
    setBusy(true)
    setCreateError(null)
    try {
      const { tag } = await api<{ tag: TagSummary }>('POST', '/tags', { name: trimmed })
      onCreated(tag)
      if (!full) onChange([...selected, tag.id])
      setName('')
    } catch (e) {
      setCreateError(e instanceof ApiError ? (e.fields.name ?? e.message) : 'Could not create the tag. Try again.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <fieldset className="group">
      <legend>Tags</legend>
      {tags.length > 0 && (
        <div className="filters" role="group" aria-label="Choose tags">
          {tags.map((t) => {
            const on = selected.includes(t.id)
            return (
              <label key={t.id} className={`chip${on ? ' selected' : ''}`}>
                <input type="checkbox" checked={on} disabled={!on && full}
                  onChange={(e) => onChange(e.target.checked ? [...selected, t.id] : selected.filter((id) => id !== t.id))} />
                <TagChip tag={t} />
              </label>
            )
          })}
        </div>
      )}
      {error && <small className="error">{error}</small>}
      <div className="kv-row">
        <input aria-label="New tag name" placeholder="New tag, e.g. a client's name" maxLength={MAX_TAG_NAME} value={name}
          aria-invalid={createError ? true : undefined}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            // Enter adds the tag instead of submitting the whole monitor form.
            if (e.key === 'Enter') {
              e.preventDefault()
              void create()
            }
          }} />
        <button type="button" disabled={busy || name.trim() === ''} onClick={() => void create()}>Create tag</button>
      </div>
      {createError && <small className="error row-error">{createError}</small>}
      <small className="hint">
        Group monitors by client or project, then filter the list and change several at once. Up to {MAX_TAGS_PER_MONITOR} tags per monitor.
      </small>
    </fieldset>
  )
}
