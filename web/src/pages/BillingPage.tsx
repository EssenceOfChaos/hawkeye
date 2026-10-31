import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { api, ApiError, type BillingSubscription, type Plan, type Usage } from '../api'
import { PlanDetails } from '../components/PlanDetails'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { redirectTo } from '../navigation'
import { markCheckoutReturn, reportPurchaseIfPaid } from '../purchaseConversion'

const POLL_MS = 3000
const POLL_MAX = 20 // about a minute

function statusText(s: BillingSubscription): { icon: string; text: string; tone: 'ok' | 'warn' | 'bad' | 'muted' } {
  if (!s.paid_access && s.status === 'incomplete') return { icon: '⏳', text: 'Payment not completed yet', tone: 'warn' }
  if (!s.paid_access) return { icon: '○', text: s.plan.code === 'free' ? 'Free plan' : 'Not active', tone: 'muted' }
  if (s.status === 'past_due') return { icon: '⚠', text: 'Payment failed — update your payment method', tone: 'bad' }
  if (s.status === 'trialing') return { icon: '✔', text: 'In trial', tone: 'ok' }
  return { icon: '✔', text: 'Active', tone: 'ok' }
}

export function BillingPage() {
  const { user, organization } = useAuth()
  const isOwner = organization?.role === 'owner'
  const verified = Boolean(user?.email_verified_at)
  const [params] = useSearchParams()
  const checkout = params.get('checkout')

  const [sub, setSub] = useState<BillingSubscription | null>(null)
  const [usage, setUsage] = useState<Usage | null>(null)
  const [plans, setPlans] = useState<Plan[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [waiting, setWaiting] = useState(checkout === 'success')
  const polls = useRef(0)

  const load = useCallback(async () => {
    try {
      const [s, p] = await Promise.all([
        api<{ subscription: BillingSubscription; usage: Usage }>('GET', '/billing/subscription'),
        api<{ plans: Plan[] }>('GET', '/plans'),
      ])
      setSub(s.subscription)
      setUsage(s.usage)
      setPlans(p.plans)
      setError(null)
      return s.subscription
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load billing details.')
      return null
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // After returning from Checkout nothing is granted until the provider confirms by webhook, so poll for it.
  useEffect(() => {
    if (!waiting) return
    const timer = setInterval(async () => {
      polls.current += 1
      const s = await load()
      if (s?.paid_access || polls.current >= POLL_MAX) setWaiting(false)
    }, POLL_MS)
    return () => clearInterval(timer)
  }, [waiting, load])

  // Coming back from Checkout marks the purchase as pending; it is reported to Google Ads as soon as the subscription shows as paid (see purchaseConversion.ts).
  useEffect(() => {
    if (checkout === 'success') markCheckoutReturn()
  }, [checkout])
  useEffect(() => {
    if (sub) reportPurchaseIfPaid(sub)
  }, [sub])

  async function go(kind: string, path: string, body?: unknown) {
    setBusy(kind)
    setError(null)
    try {
      const r = await api<{ url: string }>('POST', path, body)
      redirectTo(r.url)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : e instanceof Error ? e.message : 'That did not work. Try again.')
      setBusy(null)
    }
  }

  async function cancel() {
    if (!window.confirm('Cancel your subscription? You keep your paid plan until the end of the period you have paid for. Your monitors and data are not deleted.')) return
    setBusy('cancel')
    setError(null)
    try {
      await api('POST', '/billing/cancel-subscription')
      await load()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'That did not work. Try again.')
    } finally {
      setBusy(null)
    }
  }

  const status = sub ? statusText(sub) : null
  const canBuy = Boolean(sub?.billing_enabled && isOwner && verified && !sub?.paid_access)
  const whyNot = !sub
    ? ''
    : !sub.billing_enabled
      ? 'Paid plans are not available on this server yet.'
      : !verified
        ? 'Verify your email address to upgrade.'
        : !isOwner
          ? 'Only the organization owner can change the plan.'
          : ''

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.billing} />
        <h1>Plan &amp; billing</h1>
      </div>

      {checkout === 'canceled' && <p role="status">Checkout was canceled. You haven’t been charged.</p>}
      {waiting && !sub?.paid_access && (
        <Alert tone="warning" role="status">
          Thanks! We’re waiting for the payment provider to confirm your payment. This page updates automatically — your plan changes only once it’s confirmed.
        </Alert>
      )}
      {checkout === 'success' && !waiting && sub?.paid_access && <p role="status">Your {sub.plan.name} plan is active. Thank you!</p>}
      {checkout === 'success' && !waiting && sub && !sub.paid_access && (
        <Alert>
          We haven’t received confirmation yet. If you were charged, your plan will update shortly; if it doesn’t, contact support.
        </Alert>
      )}
      {error && <Alert>{error}</Alert>}
      {!sub && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {sub && status && usage && (
        <section className="card" aria-labelledby="cur-h">
          <h2 id="cur-h">Current plan: {sub.plan.name}</h2>
          <p className={`badge-line ${status.tone}`}>
            <span aria-hidden="true">{status.icon}</span> {status.text}
          </p>
          {sub.paid_access && sub.current_period_end && (
            <p>
              {sub.cancel_at_period_end
                ? <>Your plan ends on <strong>{new Date(sub.current_period_end).toLocaleDateString()}</strong>. You keep full access until then.</>
                : <>Renews on <strong>{new Date(sub.current_period_end).toLocaleDateString()}</strong>.</>}
            </p>
          )}
          <p>
            <strong>{usage.monitors_used}</strong> of <strong>{usage.monitor_limit}</strong> monitors used · checks every{' '}
            {Math.round(usage.plan.minimum_check_interval_seconds / 60)} min · {usage.plan.history_retention_days} days of history
          </p>
          {usage.over_limit && (
            <Alert>
              You have {usage.monitors_used} monitors but your plan includes {usage.monitor_limit}. Nothing has been deleted. To add monitors,
              delete {usage.monitors_used - usage.monitor_limit} or upgrade.
            </Alert>
          )}
          {sub.paid_access && isOwner && (
            <div className="row">
              {sub.can_manage && (
                <button disabled={busy !== null || !verified} onClick={() => void go('portal', '/billing/portal-session')}>
                  {busy === 'portal' ? 'Opening…' : 'Manage billing'}
                </button>
              )}
              {sub.has_subscription && !sub.cancel_at_period_end && (
                <button className="danger" disabled={busy !== null || !verified} onClick={() => void cancel()}>
                  {busy === 'cancel' ? 'Canceling…' : 'Cancel subscription'}
                </button>
              )}
            </div>
          )}
          {sub.paid_access && !isOwner && <p className="hint">Only the organization owner can manage billing.</p>}
        </section>
      )}

      {sub && plans.length > 0 && (
        <section aria-labelledby="plans-h">
          <h2 id="plans-h">Plans</h2>
          {whyNot && !sub.paid_access && <p className="hint">{whyNot}</p>}
          <ul className="plans app-plans">
            {plans.map((p) => {
              const current = p.code === sub.plan.code
              return (
                <li key={p.code} className={`plan${current ? ' featured' : ''}`}>
                  <h3>{p.name}{current && <span className="soon">Current plan</span>}</h3>
                  <PlanDetails plan={p} />
                  {!current && p.monthly_price_cents > 0 && !sub.paid_access && (
                    <button className="primary" disabled={!canBuy || busy !== null}
                      onClick={() => void go(p.code, '/billing/checkout-session', { plan: p.code })}>
                      {busy === p.code ? 'Redirecting…' : `Choose ${p.name}`}
                    </button>
                  )}
                </li>
              )
            })}
          </ul>
          {sub.paid_access && <p className="hint">To change plan, use Manage billing.</p>}
        </section>
      )}
    </>
  )
}
