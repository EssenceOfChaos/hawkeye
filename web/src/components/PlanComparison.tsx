import type { ReactNode } from 'react'
import { type Plan } from '../api'
import { formatMoney, formatPeriod } from '../format'

type Cell = string | number | boolean

/** A row of the comparison table: a label and the value for each plan. true and false are shown as a check mark and a dash, read out as Yes and No. */
interface Row { label: string; value: (p: Plan) => Cell }

/**
 * Every plan's limits and features side by side, from the plans table, so a number can never go stale. A row whose value is the same on every
 * plan says nothing about the difference, so it is left out and that feature belongs in INCLUDED instead. When a plan feature is added, add its
 * row here and, if a card should name it, to ADDITIONS in PlanDetails.tsx.
 */
const GROUPS: { title: string; rows: Row[] }[] = [
  {
    title: 'Monitoring',
    rows: [
      { label: 'Price per month', value: (p) => formatMoney(p.monthly_price_cents) },
      { label: 'Monitors', value: (p) => p.max_monitors },
      { label: 'Fastest check interval', value: (p) => formatPeriod(p.minimum_check_interval_seconds) },
      { label: 'History kept', value: (p) => `${p.history_retention_days} days` },
      { label: 'Check locations per monitor', value: (p) => p.max_regions ?? 1 },
      { label: 'Multi-step API checks', value: (p) => ((p.max_workflows ?? 0) > 0 ? `${p.max_workflows}, up to ${p.max_workflow_steps} steps` : false) },
      { label: 'Domains watched for expiry', value: (p) => p.max_domains ?? 0 },
      { label: 'Maintenance windows', value: (p) => !!p.maintenance_enabled },
    ],
  },
  {
    title: 'Status pages',
    rows: [
      { label: 'Public status pages', value: (p) => p.max_status_pages ?? 1 },
      { label: 'Your clients’ logos and colors', value: (p) => !!p.status_page_branding },
      { label: 'No HawkEye branding', value: (p) => !!p.status_page_white_label },
      { label: 'On your clients’ own domains', value: (p) => ((p.max_status_page_domains ?? 0) > 0 ? p.max_status_page_domains! : false) },
    ],
  },
  {
    title: 'Team, reports and tools',
    rows: [
      { label: 'Team members', value: (p) => p.max_team_members ?? 1 },
      { label: 'Require two-factor sign-in for your team', value: (p) => (p.max_team_members ?? 1) > 1 },
      { label: 'Monthly PDF uptime reports', value: (p) => !!p.reports_enabled },
      { label: 'Reports with your name and logo', value: (p) => !!p.branded_reports_enabled },
      { label: 'AI-drafted incident updates', value: (p) => !!p.ai_drafts_enabled },
      { label: 'AI root cause analyses per month', value: (p) => ((p.max_ai_analyses ?? 0) > 0 ? p.max_ai_analyses!.toLocaleString('en-US') : false) },
      { label: 'API and Claude (MCP) access', value: (p) => (p.api_write_enabled ? 'Read and make changes' : 'Read and add monitors') },
    ],
  },
]

/** What every plan has, said once instead of on every card. Only built features, in the words buyers shop for. */
const INCLUDED = [
  'Website, API, cron job and DNS monitoring',
  'Failures re-checked within seconds before alerting',
  'One alert, not twenty, when sites fail together or keep flapping',
  'Alerts that show what changed: status, headers and timings',
  'Response times split into DNS, connect, TLS and server time',
  'SSL certificate and domain expiry warnings',
  'Email, Slack, Teams, Discord, Telegram, PagerDuty and webhook alerts',
  'Two-factor sign-in',
]

function show(v: Cell): ReactNode {
  if (v === true) return <><span aria-hidden="true">✓</span><span className="sr-only">Yes</span></>
  if (v === false) return <><span aria-hidden="true">–</span><span className="sr-only">No</span></>
  return v
}

/**
 * Under the pricing cards: what every plan includes, always shown (the cards name only what differs), then the full comparison table behind a
 * "Compare plans" disclosure for the visitors who want every detail. The same on the landing pages and the billing page.
 */
export function PlanComparison({ plans }: { plans: Plan[] }) {
  const groups = GROUPS.map((g) => ({ ...g, rows: g.rows.filter((r) => new Set(plans.map((p) => String(r.value(p)))).size > 1) })).filter((g) => g.rows.length > 0)
  return (
    <div className="plan-compare-section">
      <div className="plan-included">
        <h3>Every plan includes</h3>
        <ul>{INCLUDED.map((i) => <li key={i}>{i}</li>)}</ul>
      </div>
      {groups.length > 0 && (
        <details className="plan-compare-details">
          <summary>Compare plans</summary>
          <div className="table-wrap plan-compare-wrap">
            <table className="plan-compare">
              <caption className="sr-only">Plan comparison</caption>
              <thead>
                <tr>
                  <td />
                  {plans.map((p) => <th key={p.code} scope="col">{p.name}</th>)}
                </tr>
              </thead>
              {groups.map((g) => (
                <tbody key={g.title}>
                  <tr className="plan-compare-group"><th scope="colgroup" colSpan={plans.length + 1}>{g.title}</th></tr>
                  {g.rows.map((r) => (
                    <tr key={r.label}>
                      <th scope="row">{r.label}</th>
                      {plans.map((p) => <td key={p.code}>{show(r.value(p))}</td>)}
                    </tr>
                  ))}
                </tbody>
              ))}
            </table>
          </div>
        </details>
      )}
    </div>
  )
}
