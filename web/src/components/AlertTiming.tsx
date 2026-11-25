import { useState } from 'react'
import type { QuietHours, RecheckSetting } from '../api'
import { localTimeZone } from '../format'
import { Toggle } from './Toggle'

/** The waits offered before announcing an outage, in minutes. */
const DELAYS = [0, 5, 10, 15, 30, 60, 120]

function timeZones(current: string): string[] {
  let all: string[] = []
  try {
    all = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  } catch {
    all = []
  }
  return all.includes(current) ? all : [current, ...all]
}

const delayLabel = (m: number) => (m === 0 ? 'As soon as an outage is confirmed' : m < 60 ? `After ${m} minutes down` : `After ${m / 60} hour${m === 60 ? '' : 's'} down`)

/**
 * When this monitor's alerts go out: an optional wait before an outage is announced (a short blip then alerts nobody), and quiet hours during which
 * alerts are held until morning. Both apply to every destination that receives this monitor's alerts.
 */
export function AlertTiming({ delay, onDelay, quiet, onQuiet, recheck, flap, errors }: {
  delay: number
  onDelay: (minutes: number) => void
  quiet: QuietHours | null
  onQuiet: (q: QuietHours | null) => void
  /** Checked monitors only (not heartbeats): the immediate re-check before alerting. */
  recheck?: RecheckProps
  /** Flap suppression: one "flapping" alert instead of one per outage when the monitor keeps going down and coming back. */
  flap?: { value: boolean; onChange: (on: boolean) => void }
  errors: Record<string, string | undefined>
}) {
  const options = DELAYS.includes(delay) ? DELAYS : [...DELAYS, delay].sort((a, b) => a - b)
  return (
    <fieldset className="group">
      <legend>Alert timing</legend>
      {recheck && <Recheck {...recheck} error={errors.recheck} />}
      <div className="field">
        <label htmlFor="alert-delay">Alert me</label>
        <select id="alert-delay" value={delay} onChange={(e) => onDelay(Number(e.target.value))} aria-invalid={errors.alert_delay_minutes ? true : undefined}>
          {options.map((m) => <option key={m} value={m}>{delayLabel(m)}</option>)}
        </select>
        {errors.alert_delay_minutes && <small className="error">{errors.alert_delay_minutes}</small>}
        <small className="hint">
          Counted from the first failed check. If the site is back before then, nobody is alerted about it. Slow-response and certificate warnings are not delayed.
        </small>
      </div>

      {flap && (
        <div className="field">
          <Toggle checked={flap.value} onToggle={flap.onChange}>Hold back alerts while it’s flapping</Toggle>
          <small className="hint">
            {flap.value
              ? 'After 3 outages in an hour, you get one “flapping” alert instead of one per outage, then a message once it has stayed up for 30 minutes. If it stays down, you’re alerted at once.'
              : 'Every outage and recovery is alerted, however often it goes down and comes back.'}
          </small>
          {errors.flap_suppression && <small className="error">{errors.flap_suppression}</small>}
        </div>
      )}

      <Toggle checked={quiet !== null} onToggle={(on) => onQuiet(on ? { start: '22:00', end: '07:00', timezone: localTimeZone() } : null)}>Quiet hours for this monitor</Toggle>
      {quiet && (
        <>
          <div className="kv-row">
            <label htmlFor="quiet-start" className="sr-only">Quiet hours start</label>
            <input id="quiet-start" type="time" required value={quiet.start} onChange={(e) => onQuiet({ ...quiet, start: e.target.value })}
              aria-invalid={errors['quiet_hours.start'] ? true : undefined} />
            <span>to</span>
            <label htmlFor="quiet-end" className="sr-only">Quiet hours end</label>
            <input id="quiet-end" type="time" required value={quiet.end} onChange={(e) => onQuiet({ ...quiet, end: e.target.value })}
              aria-invalid={errors['quiet_hours.end'] ? true : undefined} />
          </div>
          <div className="field">
            <label htmlFor="quiet-tz">Time zone</label>
            <select id="quiet-tz" value={quiet.timezone} onChange={(e) => onQuiet({ ...quiet, timezone: e.target.value })}
              aria-invalid={errors['quiet_hours.timezone'] ? true : undefined}>
              {timeZones(quiet.timezone).map((z) => <option key={z} value={z}>{z.replaceAll('_', ' ')}</option>)}
            </select>
          </div>
          {(errors['quiet_hours.start'] ?? errors['quiet_hours.end'] ?? errors['quiet_hours.timezone']) && (
            <small className="error row-error">{errors['quiet_hours.start'] ?? errors['quiet_hours.end'] ?? errors['quiet_hours.timezone']}</small>
          )}
          <small className="hint">
            Every day in this window, alerts for this monitor are held and sent when it ends. If the problem is over by then, nobody is woken for it.
            Use it for sites that can wait until morning, not for the ones you need to hear about at night.
          </small>
        </>
      )}
    </fieldset>
  )
}

export interface RecheckProps {
  value: RecheckSetting
  onChange: (v: RecheckSetting) => void
  /** A request (or a step) uses POST, PUT, PATCH or DELETE, so a re-check would send it again. */
  writes: boolean
  /** Checked every 30 seconds: the next check comes about as soon as a re-check would. */
  fast: boolean
}

/**
 * The immediate re-check: a failed check is retried 15 seconds later, and only a second failure counts as an outage. It is on unless the request may
 * change something ("auto" stays off for POST, PUT, PATCH and DELETE); turning it on for such a request stores "always".
 */
function Recheck({ value, onChange, writes, fast, error }: RecheckProps & { error?: string }) {
  const on = value === 'always' || (value === 'auto' && !writes)
  let hint = 'A failed check is retried 15 seconds later, and only a second failure counts as an outage: faster alerts, and fewer false alarms.'
  if (writes && !on) {
    hint = 'Off for this monitor: it sends a request that may change data, and a re-check would send it again. Turn it on only if repeating the request is safe.'
  } else if (writes) {
    hint = 'A failed check is retried 15 seconds later, which sends the request again. Keep this on only if repeating it is safe.'
  }
  if (fast && on) hint = 'Checks every 30 seconds aren’t re-checked: the next check comes just as soon.'
  return (
    <div className="field">
      <Toggle checked={on} onToggle={(v) => onChange(v ? (writes ? 'always' : 'auto') : 'off')}>Re-check right away before alerting</Toggle>
      <small className="hint">{hint}</small>
      {error && <small className="error">{error}</small>}
    </div>
  )
}

/** What a bulk alert-timing change sets: only the fields present change; quiet_hours null turns them off. */
export type AlertTimingChange = { alert_delay_minutes?: number; quiet_hours?: QuietHours | null }

/**
 * Sets the alert delay and/or quiet hours of the selected monitors at once. Each starts at "keep", so only what is chosen changes on each monitor.
 */
export function BulkAlertTiming({ onApply, onCancel }: { onApply: (change: AlertTimingChange) => void; onCancel: () => void }) {
  const [delay, setDelay] = useState('') // '' keeps each monitor's delay
  const [quietMode, setQuietMode] = useState<'keep' | 'off' | 'set'>('keep')
  const [quiet, setQuiet] = useState<QuietHours>({ start: '22:00', end: '07:00', timezone: localTimeZone() })
  const change: AlertTimingChange = {}
  if (delay !== '') change.alert_delay_minutes = Number(delay)
  if (quietMode === 'off') change.quiet_hours = null
  if (quietMode === 'set') change.quiet_hours = quiet
  const nothing = Object.keys(change).length === 0
  return (
    <fieldset className="group bulk-timing">
      <legend>Alert timing for the selected monitors</legend>
      <div className="row">
        <label htmlFor="bulk-delay">Alert</label>
        <select id="bulk-delay" value={delay} onChange={(e) => setDelay(e.target.value)}>
          <option value="">Keep each monitor’s delay</option>
          {DELAYS.map((m) => <option key={m} value={m}>{delayLabel(m)}</option>)}
        </select>
      </div>
      <div className="row">
        <label htmlFor="bulk-quiet">Quiet hours</label>
        <select id="bulk-quiet" value={quietMode} onChange={(e) => setQuietMode(e.target.value as 'keep' | 'off' | 'set')}>
          <option value="keep">Keep each monitor’s quiet hours</option>
          <option value="set">Set quiet hours</option>
          <option value="off">Turn quiet hours off</option>
        </select>
        {quietMode === 'set' && (
          <>
            <label htmlFor="bulk-quiet-start" className="sr-only">Quiet hours start</label>
            <input id="bulk-quiet-start" type="time" required value={quiet.start} onChange={(e) => setQuiet({ ...quiet, start: e.target.value })} />
            <span>to</span>
            <label htmlFor="bulk-quiet-end" className="sr-only">Quiet hours end</label>
            <input id="bulk-quiet-end" type="time" required value={quiet.end} onChange={(e) => setQuiet({ ...quiet, end: e.target.value })} />
            <label htmlFor="bulk-quiet-tz" className="sr-only">Time zone</label>
            <select id="bulk-quiet-tz" value={quiet.timezone} onChange={(e) => setQuiet({ ...quiet, timezone: e.target.value })}>
              {timeZones(quiet.timezone).map((z) => <option key={z} value={z}>{z.replaceAll('_', ' ')}</option>)}
            </select>
          </>
        )}
      </div>
      <div className="row">
        <button type="button" className="primary" disabled={nothing} onClick={() => onApply(change)}>Apply</button>
        <button type="button" className="linklike" onClick={onCancel}>Cancel</button>
      </div>
    </fieldset>
  )
}
