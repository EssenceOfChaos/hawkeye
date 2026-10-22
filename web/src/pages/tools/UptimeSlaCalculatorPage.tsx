import { useState } from 'react'
import { Field } from '../../components/Field'
import { ToolShell } from '../../components/ToolShell'
import { allowedDowntimeSeconds, COMMON_TARGETS, formatDuration, parseNumber, PERIODS, SECONDS_PER, uptimeFromDowntime, type Period } from '../../tools/sla'
import { useToolUse } from '../../tools/useToolTracking'

const NINES: Record<number, string> = { 99: 'two nines', 99.9: 'three nines', 99.99: 'four nines', 99.999: 'five nines' }

/** Cuts a percentage to `digits` decimals without rounding up, so a shortfall never shows as a pass (99.99996 is not "100.0000"). */
const truncate = (n: number, digits: number) => (Math.floor(n * 10 ** digits) / 10 ** digits).toFixed(digits)

export function UptimeSlaCalculatorPage() {
  const used = useToolUse('uptime-sla-calculator')
  const [target, setTarget] = useState('99.9')
  const [downtime, setDowntime] = useState('45')
  const [period, setPeriod] = useState<Period>('month')

  const targetValue = parseNumber(target)
  const targetOk = targetValue !== null && targetValue >= 0 && targetValue <= 100

  const downtimeMinutes = parseNumber(downtime)
  const downtimeSeconds = downtimeMinutes === null ? null : downtimeMinutes * 60
  const downtimeError =
    downtimeSeconds === null || downtimeSeconds < 0 ? 'Enter the minutes of downtime, for example 45.' : downtimeSeconds > SECONDS_PER[period] ? 'That is longer than the period.' : undefined
  const achieved = downtimeError || downtimeSeconds === null ? null : uptimeFromDowntime(downtimeSeconds, period)

  return (
    <ToolShell
      tool="uptime-sla-calculator"
      title="Uptime SLA Calculator"
      intro="See how much downtime an uptime percentage allows per day, week, month, quarter and year, or work out the uptime you actually delivered."
      cta={{
        heading: 'Know your real uptime',
        text: 'HawkEye checks your website or API on a schedule, alerts you when an outage is confirmed, and reports uptime from the checks it ran, so you are not guessing against your SLA.',
      }}
    >
      <section className="card" aria-labelledby="sla-target-h">
        <h2 id="sla-target-h">How much downtime does my target allow?</h2>
        <Field
          label="Uptime target (%)"
          inputMode="decimal"
          value={target}
          onChange={(e) => { used(); setTarget(e.target.value) }}
          hint="For example 99.9"
          error={targetOk ? undefined : 'Enter a percentage from 0 to 100.'}
        />
        {targetOk && (
          <dl className="tool-results" aria-live="polite">
            {PERIODS.map((p) => (
              <div key={p.key}>
                <dt>{p.label}</dt>
                <dd className="mono">{formatDuration(allowedDowntimeSeconds(targetValue, p.key))}</dd>
              </div>
            ))}
          </dl>
        )}
      </section>

      <section aria-labelledby="sla-table-h">
        <h2 id="sla-table-h">Common uptime targets</h2>
        <div className="table-wrap">
          <table className="tool-table">
            <caption className="sr-only">Downtime allowed by common uptime targets</caption>
            <thead>
              <tr>
                <th scope="col">Uptime</th>
                {PERIODS.map((p) => <th scope="col" key={p.key}>{p.label}</th>)}
              </tr>
            </thead>
            <tbody>
              {COMMON_TARGETS.map((t) => (
                <tr key={t}>
                  <th scope="row">{t}%{NINES[t] && <span className="hint"> ({NINES[t]})</span>}</th>
                  {PERIODS.map((p) => <td className="mono" key={p.key}>{formatDuration(allowedDowntimeSeconds(t, p.key))}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="card" aria-labelledby="sla-actual-h">
        <h2 id="sla-actual-h">What uptime did I deliver?</h2>
        <div className="tool-grid">
          <Field label="Downtime (minutes)" inputMode="decimal" value={downtime} onChange={(e) => { used(); setDowntime(e.target.value) }} error={downtimeError} />
          <div className="field">
            <label htmlFor="sla-period">Over the</label>
            <select id="sla-period" value={period} onChange={(e) => { used(); setPeriod(e.target.value as Period) }}>
              {PERIODS.map((p) => <option key={p.key} value={p.key}>{p.key}</option>)}
            </select>
          </div>
        </div>
        {achieved !== null && (
          <div aria-live="polite">
            <p className="tool-figure mono">{truncate(achieved, 4)}% uptime</p>
            <p className="hint">
              Meets:{' '}
              {COMMON_TARGETS.map((t, i) => (
                <span key={t}>
                  {i > 0 && ' '}
                  <span className={achieved >= t ? 'tool-pass' : 'tool-fail'}>{t}% {achieved >= t ? '✓' : '✗'}</span>
                </span>
              ))}
            </p>
          </div>
        )}
      </section>

      <section aria-labelledby="sla-how-h">
        <h2 id="sla-how-h">How it is calculated</h2>
        <p>
          Allowed downtime = period × (100 − uptime %) ÷ 100. This calculator uses a 365-day year and a month of one twelfth of a year (about 30.4 days). Calculators that use a 30-day month or a 365.25-day year give slightly different figures, so check which one your
          contract uses.
        </p>
        <p>
          An SLA is a contract, and the contract decides what counts: the measurement window, whether planned maintenance is excluded, and where the
          measurement is taken. Treat these figures as arithmetic to plan with, not as a reading of anyone’s agreement.
        </p>
        <details className="explain">
          <summary>Why does each extra nine matter so much?</summary>
          <p>
            Each extra nine cuts the allowed downtime to a tenth. 99.9% allows about 8 hours 46 minutes a year; 99.99% allows under an hour; 99.999% allows about
            5 minutes. A single outage can use up a whole year of a four-nines budget, which is why detecting an outage quickly matters as much as the target.
          </p>
        </details>
      </section>
    </ToolShell>
  )
}
