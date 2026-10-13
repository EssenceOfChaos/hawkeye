# Billing (Stripe)

Subscriptions are handled with Stripe Checkout (hosted), the Stripe customer portal, and signed webhooks. The application never sees card data.

## What is verified, and what is not

Built and tested without Stripe credentials:
- webhook signature verification using Stripe's official library (signed in tests with the library's own signer), duplicate/out-of-order/replay handling;
- subscription-state → entitlement mapping, downgrade behaviour, role/email gating, server-side pricing;
- that our Stripe provider sends the right requests: the official SDK is pointed at a local stand-in server and the exact form fields are asserted
  (`internal/billing/stripe_provider_test.go`).

**Not verified until you have test keys:** that Stripe's real API accepts those requests and returns the shapes we parse, your Dashboard configuration,
and the real webhook delivery. Do the checklist below once with a test-mode key before relying on any of it.

## Setup (test mode first)

1. **Keys.** Dashboard (test mode) → Developers → API keys: copy the secret key (`sk_test_…`). Put it in `.env` as `STRIPE_SECRET_KEY`.
   Live keys (`sk_live_…`/`rk_live_…`) are refused at startup unless `APP_ENV=production`.
2. **Products & prices.** Create two recurring monthly prices (Pro $19, Agency $59, or whatever you decide) and copy their IDs
   (`price_…`) into `STRIPE_PRICE_PRO_ID` and `STRIPE_PRICE_AGENCY_ID`. The *plan limits* live in our `plans` table; the Stripe price only decides
   what is charged. A price that is not one of those two IDs never grants a plan.
3. **Customer portal.** Dashboard → Settings → Billing → Customer portal: save a configuration (test mode and live mode are separate) and enable
   cancellation and plan switching as you want them. The portal session API needs this configuration to exist.
4. **Webhooks, local development.** Install the Stripe CLI, `stripe login`, then
   `stripe listen --forward-to localhost:8080/api/v1/webhooks/stripe`. It prints `Ready! Your webhook signing secret is 'whsec_…'`; put that in
   `STRIPE_WEBHOOK_SECRET` and restart the API and worker. (The CLI's secret differs from a Dashboard endpoint's.)
5. **Webhooks, deployed.** Workbench → Webhooks → create an event destination for *your account*, payload format **Snapshot** (this code reads snapshot
   events, not thin events), URL `https://<your-domain>/api/v1/webhooks/stripe`, and subscribe to only:
   `customer.subscription.created`, `customer.subscription.updated`, `customer.subscription.deleted`, `checkout.session.completed`.
   Copy the endpoint's signing secret into `STRIPE_WEBHOOK_SECRET`. Stripe recommends also allowlisting [Stripe's IP addresses](https://docs.stripe.com/ips) at your
   firewall in addition to signature verification. Roll the secret periodically (Workbench lets the old one stay valid up to 24 h).
6. **Both processes need the settings.** The API creates sessions and receives webhooks; the worker retries unprocessed events and also needs the price IDs
   and key (it re-reads subscriptions from Stripe).

Billing is simply off while no Stripe settings are present (`/plans` reports `billing_enabled: false`, the UI says paid plans are not available yet).
A *partial* configuration (e.g. a key without a webhook secret) stops startup with a clear message.

### Test cards (from Stripe's testing page)

Any future expiry (e.g. `12/34`), any 3-digit CVC.

| Card | Use it to test |
|---|---|
| `4242 4242 4242 4242` | successful payment → plan becomes active |
| `4000 0000 0000 0341` | card attaches, **later charges fail** → renewal failure → `past_due` |
| `4000 0000 0000 0002` | generic decline at checkout |
| `4000 0000 0000 9995` | insufficient funds decline |
| `4000 0000 0000 3220` | requires 3-D Secure authentication |

Use a different card per independent scenario (Stripe notes card-level state can carry between tests).

## Flow

```
Browser ──POST /billing/checkout-session {plan}──▶ API  (owner + verified email; price from config; nothing granted)
Browser ◀── redirect URL ── API ;  Browser ──▶ Stripe Checkout ──▶ back to /billing?checkout=success   (still nothing granted)
Stripe ──POST /api/v1/webhooks/stripe──▶ API: verify signature on the RAW body → store event (UNIQUE event id) → 200 right away
        API/worker: for a subscription event, RE-READ the subscription from Stripe → apply to our subscriptions row (one transaction)
UI polls GET /billing/subscription until paid access appears (it says "waiting for the payment provider to confirm" meanwhile)
```

**Why re-read instead of trusting the event?** Stripe's docs state that events are not delivered in order, can share the same `created` second, and
that `created` must not be used to decide order. An event is therefore only a *signal that a subscription changed*; the state we apply is what Stripe says
the subscription is *now*. Stale, duplicated, delayed or reordered events all converge on the same result (`TestReorderedAndStaleEventsCannotRegressState`).
If Stripe cannot be reached the event stays pending and is retried (30 s, 2 m, 10 m, 1 h, then hourly) up to 8 attempts, then is marked `failed` (an alert metric).
A *deleted* event is the one exception: if the subscription can no longer be retrieved, the signed deletion event itself ends paid access.

## Subscription status → entitlements

| Stripe status | Our status | Paid access? | Notes |
|---|---|---|---|
| `active` | `active` | yes | |
| `trialing` | `trialing` | yes | |
| `past_due` | `past_due` | yes (grace) | payment failed; Stripe is retrying. The UI asks the owner to fix the payment method in the portal |
| `incomplete` | `incomplete` | **no** | first payment not completed; Checkout success alone grants nothing |
| `unpaid` | `canceled` | **no** | Stripe: revoke access when unpaid |
| `canceled` | `canceled` | **no** | |
| `incomplete_expired` | `canceled` | **no** | |
| `paused` | `canceled` | **no** | |
| anything else/unknown | `canceled` | **no** | fails closed |

"No paid access" means the organization falls back to the Free plan's limits. **Nothing is deleted:** monitors and history stay; creating more monitors is blocked while over the
Free limit, the dashboard explains how many to delete or that the owner can upgrade, and the retention job later trims history to the Free window (documented in `architecture.md`).
Cancelling from the app (or the portal) sets *cancel at period end*: the owner keeps paid access until the period ends, then Stripe deletes the subscription and access ends.

## Safety rules enforced in code

- **Prices come from configuration only**; the checkout endpoint reads just the plan name (`pro`/`agency`). Tested: client-supplied `price`/`amount` fields are ignored.
- **Only owners** can start checkout, open the portal, cancel or re-sync, and the account email must be **verified** (members can view the plan).
- **One subscription per organization.** A second paid subscription is reported as a conflict (event retried then `failed`) rather than overwriting the first; a stray non-paying
  duplicate being cancelled is ignored. Choosing a plan while subscribed returns `ALREADY_SUBSCRIBED` (use the portal). One Stripe customer maps to exactly one organization (unique index).
- **Unknown prices never grant access.** A subscription whose price isn't `STRIPE_PRICE_PRO_ID`/`STRIPE_PRICE_AGENCY_ID` is rejected (and a cancellation of one is still honoured).
- **Webhook endpoint**: signature checked on the raw body before anything is stored (5-minute replay tolerance); invalid → 400 and nothing stored; valid → stored durably
  and acknowledged; duplicates acknowledged without a second effect; no session or CSRF token needed (registered outside the CSRF-protected group). Body limit 1 MB.
- Provider errors are logged in full and returned to users as a generic `BILLING_PROVIDER_ERROR`; secrets are never logged. Test keys only outside production.

## Operations

- `POST /api/v1/billing/sync` (owner) re-reads the organization's subscription from Stripe and applies it: the repair tool when webhooks were missed (e.g. an outage longer than Stripe's 3-day retry window).
- Stripe's Workbench → Webhooks → *Event deliveries* shows delivery status and can resend events (15 days via Dashboard, 30 days via CLI `stripe events resend`).
- Metrics, alerts and SQL for stuck events: `docs/operations.md` (Billing).
- Stored webhook payloads (which include customer details) are deleted after 90 days (failed ones after 180).
