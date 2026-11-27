import { type BillingInterval, type Plan } from '../api'
import { formatMoney, formatPeriod } from '../format'
import { monthsFree, planTagline } from '../planCopy'

/**
 * Monthly or yearly prices on the pricing cards. Only shown when the server offers yearly billing. The saving named on the yearly option is the
 * smallest any paid plan gives, so it never promises more than every plan delivers.
 */
export function IntervalToggle({ value, onChange, plans }: { value: BillingInterval; onChange: (v: BillingInterval) => void; plans: Plan[] }) {
  const savings = plans.filter((p) => p.monthly_price_cents > 0 && p.annual_price_cents != null).map(monthsFree)
  const least = savings.length ? Math.min(...savings) : 0
  return (
    <div className="filters interval-toggle" role="radiogroup" aria-label="Billing period">
      {(['month', 'year'] as const).map((v) => (
        <label key={v} className={`chip${value === v ? ' selected' : ''}`}>
          <input type="radio" name="billing-interval" value={v} checked={value === v} onChange={() => onChange(v)} />
          {v === 'month' ? 'Monthly' : least > 0 ? `Yearly · ${least} month${least === 1 ? '' : 's'} free` : 'Yearly'}
        </label>
      ))}
    </div>
  )
}

const seats = (p: Plan) => p.max_team_members ?? 1
const regions = (p: Plan) => p.max_regions ?? 1

/**
 * The plan features a card can name in its "Everything in <previous plan>, plus" list: each is on or off per plan. A card lists only the ones its
 * plan has and the plan before it does not, so the cards show what differs between plans. Everything else (limits that every plan has, and the
 * features all plans share) is in the comparison table, PlanComparison.tsx.
 */
const ADDITIONS: { has: (p: Plan) => boolean; label: (p: Plan) => string }[] = [
  { has: (p) => seats(p) > 1, label: (p) => `${seats(p)} team members, with two-factor sign-in you can require` },
  { has: (p) => !!p.maintenance_enabled, label: () => 'Maintenance windows and quiet deploys from CI' },
  { has: (p) => !!p.api_write_enabled, label: () => 'API and Claude can make changes, not just read' },
  { has: (p) => !!p.status_page_branding, label: () => 'Status pages with your clients’ logos and colors' },
  { has: (p) => !!p.status_page_white_label, label: () => 'White-label status pages, no HawkEye branding' },
  { has: (p) => (p.max_status_page_domains ?? 0) > 0, label: () => 'Status pages on your clients’ own domains' },
  { has: (p) => !!p.reports_enabled, label: () => 'Monthly PDF uptime reports' },
  { has: (p) => !!p.branded_reports_enabled, label: () => 'Reports with your name and logo' },
  { has: (p) => !!p.ai_drafts_enabled, label: () => 'AI-drafted incident updates' },
]

/**
 * Everything on a pricing card below the plan's name: the tagline, the price, the four limits that differ on every plan, and what the plan adds
 * over the one before it. The landing page, the monitoring landing page and the billing page all render this, so a plan change is made in one place
 * and the three can never disagree (PlanCardsInSync.test.tsx guards it). `previous` is the next cheaper plan, or none for the cheapest.
 */
export function PlanDetails({ plan, previous, interval = 'month' }: { plan: Plan; previous?: Plan; interval?: BillingInterval }) {
  const tagline = planTagline(plan.code)
  const yearly = interval === 'year' && plan.annual_price_cents != null && plan.monthly_price_cents > 0
  const free = monthsFree(plan)
  const added = previous ? ADDITIONS.filter((a) => a.has(plan) && !a.has(previous)) : []
  return (
    <>
      {tagline && <p className="plan-tagline">{tagline}</p>}
      {yearly ? (
        <>
          <p className="price">
            <strong>{formatMoney(plan.annual_price_cents!)}</strong>
            <span> / year</span>
          </p>
          <p className="hint price-note">
            {free > 0 ? `${free} month${free === 1 ? '' : 's'} free · ` : ''}billed once a year instead of {formatMoney(plan.monthly_price_cents)} a month
          </p>
        </>
      ) : (
        <p className="price">
          <strong>{formatMoney(plan.monthly_price_cents)}</strong>
          {plan.monthly_price_cents > 0 && <span> / month</span>}
        </p>
      )}
      <ul className="plan-limits">
        <li>{plan.max_monitors} monitors</li>
        <li>Checks every {formatPeriod(plan.minimum_check_interval_seconds)}</li>
        <li>{plan.history_retention_days} days of history</li>
        <li>{regions(plan) > 1 ? `Checks from up to ${regions(plan)} locations` : 'Checks from 1 location'}</li>
      </ul>
      {added.length > 0 && (
        <>
          <p className="plan-plus">Everything in {previous!.name}, plus:</p>
          <ul className="plan-adds">
            {added.map((a) => <li key={a.label(plan)}>{a.label(plan)}</li>)}
          </ul>
        </>
      )}
      <span className="plan-spacer" />
    </>
  )
}
