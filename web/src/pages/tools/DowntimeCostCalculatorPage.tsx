import { useState } from 'react'
import { Field } from '../../components/Field'
import { ToolShell } from '../../components/ToolShell'
import { estimateDowntimeCost, inRange, LIMITS, type CostInput } from '../../tools/downtimeCost'
import { parseNumber } from '../../tools/sla'
import { useToolUse } from '../../tools/useToolTracking'

type Fields = Record<'dailyRevenue' | 'businessHours' | 'outageHours' | 'outageMinutes' | 'percentAffected' | 'otherCostPerHour' | 'outagesPerYear', string>

const money = (n: number) => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: n === 0 || n >= 100 ? 0 : 2 }).format(n)

export function DowntimeCostCalculatorPage() {
  const used = useToolUse('downtime-cost-calculator')
  const [f, setF] = useState<Fields>({ dailyRevenue: '5000', businessHours: '24', outageHours: '1', outageMinutes: '30', percentAffected: '100', otherCostPerHour: '0', outagesPerYear: '2' })
  const set = (name: keyof Fields) => (e: React.ChangeEvent<HTMLInputElement>) => {
    used()
    setF((prev) => ({ ...prev, [name]: e.target.value }))
  }

  const n = {
    dailyRevenue: parseNumber(f.dailyRevenue),
    businessHours: parseNumber(f.businessHours),
    outageHours: parseNumber(f.outageHours),
    outageMinutes: parseNumber(f.outageMinutes),
    percentAffected: parseNumber(f.percentAffected),
    otherCostPerHour: parseNumber(f.otherCostPerHour),
    outagesPerYear: parseNumber(f.outagesPerYear),
  }
  const outage = n.outageHours === null || n.outageMinutes === null ? null : n.outageHours + n.outageMinutes / 60
  const errors: Partial<Record<keyof Fields, string>> = {
    dailyRevenue: n.dailyRevenue !== null && inRange(n.dailyRevenue, LIMITS.dailyRevenue) ? undefined : 'Enter an amount in dollars, for example 5000.',
    businessHours: n.businessHours !== null && inRange(n.businessHours, LIMITS.businessHours) ? undefined : 'Enter hours from 1 to 24.',
    outageHours: n.outageHours !== null && n.outageHours >= 0 ? undefined : 'Enter whole or part hours, for example 2.',
    outageMinutes: n.outageMinutes !== null && n.outageMinutes >= 0 ? undefined : 'Enter minutes, for example 30.',
    percentAffected: n.percentAffected !== null && inRange(n.percentAffected, LIMITS.percentAffected) ? undefined : 'Enter a percentage from 0 to 100.',
    otherCostPerHour: n.otherCostPerHour !== null && inRange(n.otherCostPerHour, LIMITS.otherCostPerHour) ? undefined : 'Enter an amount in dollars, or 0.',
    outagesPerYear: n.outagesPerYear !== null && inRange(n.outagesPerYear, LIMITS.outagesPerYear) ? undefined : 'Enter a number of outages, or 0.',
  }
  const outageError = outage !== null && !inRange(outage, LIMITS.outageHours) ? 'The outage cannot be longer than a year.' : undefined
  const valid = Object.values(errors).every((e) => e === undefined) && outage !== null && !outageError

  const result = valid
    ? estimateDowntimeCost({
        dailyRevenue: n.dailyRevenue!,
        businessHours: n.businessHours!,
        outageHours: outage,
        percentAffected: n.percentAffected!,
        otherCostPerHour: n.otherCostPerHour!,
        outagesPerYear: n.outagesPerYear!,
      } satisfies CostInput)
    : null

  return (
    <ToolShell
      tool="downtime-cost-calculator"
      title="Downtime Cost Calculator"
      intro="Estimate the revenue a website outage puts at risk from your daily revenue, the hours you trade and how long the site is down."
      cta={{
        heading: 'Hear about an outage in minutes, not from customers',
        text: 'HawkEye checks your website or API on a schedule and alerts you by email, Slack or webhook when an outage is confirmed, so you can start fixing it sooner.',
      }}
    >
      <section className="card" aria-labelledby="cost-inputs-h">
        <h2 id="cost-inputs-h">Your business</h2>
        <div className="tool-grid">
          <Field label="Average revenue per day ($)" inputMode="decimal" value={f.dailyRevenue} onChange={set('dailyRevenue')} error={errors.dailyRevenue} hint="Revenue that depends on your website." />
          <Field label="Hours per day you take orders" inputMode="decimal" value={f.businessHours} onChange={set('businessHours')} error={errors.businessHours} hint="24 for a store that is always open." />
          <Field label="Revenue the outage stops (%)" inputMode="decimal" value={f.percentAffected} onChange={set('percentAffected')} error={errors.percentAffected} hint="100 if nobody can buy; less if only part is down." />
          <Field label="Other cost per hour of outage ($)" inputMode="decimal" value={f.otherCostPerHour} onChange={set('otherCostPerHour')} error={errors.otherCostPerHour} hint="Optional: staff time, support, SLA credits." />
        </div>
        <h2>The outage</h2>
        <div className="tool-grid">
          <Field label="Hours" inputMode="decimal" value={f.outageHours} onChange={set('outageHours')} error={errors.outageHours} />
          <Field label="Minutes" inputMode="decimal" value={f.outageMinutes} onChange={set('outageMinutes')} error={errors.outageMinutes ?? outageError} />
          <Field label="Outages like this per year" inputMode="decimal" value={f.outagesPerYear} onChange={set('outagesPerYear')} error={errors.outagesPerYear} hint="To project a yearly figure." />
        </div>
      </section>

      <section className="card" aria-labelledby="cost-result-h" aria-live="polite">
        <h2 id="cost-result-h">Estimated cost of this outage</h2>
        {result ? (
          <>
            <p className="tool-figure mono">{money(result.total)}</p>
            <dl className="tool-results">
              <div><dt>Revenue at risk</dt><dd className="mono">{money(result.revenueLost)}</dd></div>
              <div><dt>Other costs</dt><dd className="mono">{money(result.otherCost)}</dd></div>
              <div><dt>Revenue at risk per minute</dt><dd className="mono">{money(result.revenueLostPerMinute)}</dd></div>
              <div><dt>Per year, at {n.outagesPerYear} outage{n.outagesPerYear === 1 ? '' : 's'} like this</dt><dd className="mono">{money(result.annualExposure)}</dd></div>
            </dl>
          </>
        ) : (
          <p className="hint">Fix the highlighted fields to see the estimate.</p>
        )}
      </section>

      <section aria-labelledby="cost-how-h">
        <h2 id="cost-how-h">How it is calculated</h2>
        <p>
          Revenue per hour = daily revenue ÷ business hours. Revenue at risk = revenue per hour × outage hours × the share of revenue the outage stops. Other
          costs = cost per hour × outage hours. The yearly figure is the total for one outage times the outages you expect.
        </p>
        <p>
          This is an estimate of exposure, not an accounting figure. It spreads revenue evenly across your trading hours, so an outage at your busiest hour
          costs more and one overnight costs less. Some customers come back and buy later, which this does not count; it also does not count lost trust or
          search ranking, which are real but hard to put a number on.
        </p>
        <details className="explain">
          <summary>Where do I find my daily revenue?</summary>
          <p>
            Take the revenue your website generated over a recent period, for example the last 90 days, and divide by the number of days. Use only the revenue that
            depends on the site being up: orders, bookings, subscriptions. If your sales are seasonal, run the calculator for a busy day as well as an average one.
          </p>
        </details>
      </section>
    </ToolShell>
  )
}
