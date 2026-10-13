import { RANGE_LABELS } from '../format'

interface Props {
  value: string
  options: string[]
  onChange: (range: string) => void
}

/** The single filter row above charts. Presets only, as a radio group so it is keyboard- and screen-reader friendly. */
export function RangePicker({ value, options, onChange }: Props) {
  return (
    <div className="filters" role="radiogroup" aria-label="Time range">
      {options.map((o) => (
        <label key={o} className={`chip${o === value ? ' selected' : ''}`}>
          <input type="radio" name="range" value={o} checked={o === value} onChange={() => onChange(o)} />
          <span>{RANGE_LABELS[o] ?? o}</span>
        </label>
      ))}
    </div>
  )
}
