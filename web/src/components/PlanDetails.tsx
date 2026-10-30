import { type Plan } from '../api'
import { formatMoney } from '../format'
import { planTagline } from '../planCopy'

/**
 * Everything on a pricing card below the plan's name: the tagline, the price and the feature list. The landing page, the monitoring landing page and the
 * billing page all render this, so a new plan feature is added in one place and the three can never disagree (PlanCardsInSync.test.tsx guards it).
 */
export function PlanDetails({ plan }: { plan: Plan }) {
  const mins = Math.round(plan.minimum_check_interval_seconds / 60)
  const tagline = planTagline(plan.code)
  return (
    <>
      {tagline && <p className="plan-tagline">{tagline}</p>}
      <p className="price">
        <strong>{formatMoney(plan.monthly_price_cents)}</strong>
        {plan.monthly_price_cents > 0 && <span> / month</span>}
      </p>
      <ul>
        <li>{plan.max_monitors} website monitors</li>
        <li>Checks every {mins} minute{mins === 1 ? '' : 's'}</li>
        <li>{plan.history_retention_days} days of history</li>
        {(plan.max_regions ?? 1) > 1 && <li>Checks from up to {plan.max_regions} locations</li>}
        {(plan.max_team_members ?? 1) > 1 && <li>{plan.max_team_members} team members</li>}
        <li>Website and API monitoring</li>
        <li>SSL certificate expiry warnings</li>
        {plan.max_domains !== undefined && <li>Domain expiry warnings for up to {plan.max_domains} domains</li>}
        <li>Email, Slack and webhook alerts</li>
        <li>Public status page</li>
        {plan.maintenance_enabled && <li>Maintenance windows</li>}
        {plan.reports_enabled && <li>Monthly PDF uptime reports</li>}
        {plan.branded_reports_enabled && <li>Branded Reports with name and logo</li>}
      </ul>
    </>
  )
}
