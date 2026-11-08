import { type BillingInterval, type Plan } from '../api'
import { formatMoney } from '../format'
import { planTagline } from '../planCopy'

/** Whole months a year paid at once saves against twelve monthly payments (2 for "two months free"); 0 when it saves nothing or is not sold yearly. */
export function monthsFree(plan: Plan): number {
  if (!plan.annual_price_cents || plan.monthly_price_cents <= 0) return 0
  return Math.max(0, Math.round((plan.monthly_price_cents * 12 - plan.annual_price_cents) / plan.monthly_price_cents))
}

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

/**
 * Everything on a pricing card below the plan's name: the tagline, the price and the feature list. The landing page, the monitoring landing page and the
 * billing page all render this, so a new plan feature is added in one place and the three can never disagree (PlanCardsInSync.test.tsx guards it).
 */
export function PlanDetails({ plan, interval = 'month' }: { plan: Plan; interval?: BillingInterval }) {
  const mins = Math.round(plan.minimum_check_interval_seconds / 60)
  const tagline = planTagline(plan.code)
  const yearly = interval === 'year' && plan.annual_price_cents != null && plan.monthly_price_cents > 0
  const free = monthsFree(plan)
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
      <ul>
        <li>{plan.max_monitors} monitors</li>
        <li>Checks every {mins} minute{mins === 1 ? '' : 's'}</li>
        <li>{plan.history_retention_days} days of history</li>
        {(plan.max_regions ?? 1) > 1 && <li>Checks from up to {plan.max_regions} locations</li>}
        {(plan.max_team_members ?? 1) > 1 && <li>{plan.max_team_members} team members</li>}
        {(plan.max_team_members ?? 1) > 1 && <li>Require two-factor sign-in for your team</li>}
        <li>Website and API monitoring</li>
        <li>Cron job and DNS monitoring</li>
        <li>SSL certificate expiry warnings</li>
        {plan.max_domains !== undefined && <li>Domain expiry warnings for up to {plan.max_domains} domains</li>}
        <li>Email, Slack, Teams, Discord, Telegram and webhook alerts</li>
        <li>{plan.api_write_enabled ? 'API and Claude (MCP) access with read and write keys' : 'Read-only API and Claude (MCP) access'}</li>
        <li>{(plan.max_status_pages ?? 1) > 1 ? `${plan.max_status_pages} public status pages` : 'Public status page'}</li>
        {plan.status_page_white_label
          ? <li>White-label status pages: your clients’ logos and colors, no HawkEye branding</li>
          : plan.status_page_branding && <li>Status pages with your clients’ logos and colors</li>}
        {plan.maintenance_enabled && <li>Maintenance windows</li>}
        {plan.reports_enabled && <li>Monthly PDF uptime reports</li>}
        {plan.branded_reports_enabled && <li>Branded Reports with name and logo</li>}
      </ul>
    </>
  )
}
