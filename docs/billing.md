# Billing (Stripe)

Subscriptions are handled with Stripe Checkout (hosted), the Stripe customer portal, and signed webhooks. The application never sees card data.

## What is verified, and what is not

Verified **against the real Stripe sandbox** (`internal/billing/live_smoke_test.go`, run with `STRIPE_LIVE_SMOKE=1`, test-mode keys only, self-cleaning):
- Checkout sessions are accepted by Stripe and return a real `checkout.stripe.com` URL; customer creation with an idempotency key; customer portal sessions;
- a real subscription is read back correctly (status, price, billing period, metadata) and cancel-at-period-end works;
- **real Stripe events** (account API version `2026-09-30.endive`) are replayed byte-for-byte through our webhook pipeline with the real provider re-reading state:
  a real `customer.subscription.created` moves an organization Free → Pro, and a real `customer.subscription.deleted` moves it back, with nothing deleted.

Verified with fakes/fixtures (no network): signature verification (signed with Stripe's own SDK helper, and independently with `openssl` against the running API),
duplicates/replay/ordering, status mapping, downgrade behavior, role/email gating, server-side pricing.

**Not yet verified:** Stripe's *own* delivery of a webhook to our endpoint with *its* signature (needs `stripe listen` or a registered endpoint; step 4 below), and Checkout page completion by a browser.
The replay tests sign real payloads with a local test secret instead.

## Decisions for you (business, not technical)

- **Managed Payments.** New Stripe accounts have *Managed Payments* on by default: Stripe becomes the **merchant of record** and handles sales tax/VAT/GST in 80+ countries, fraud and
  disputes. That is attractive for a small team selling worldwide, but it is a different legal/fee arrangement and Stripe documents restrictions (no Connect, no subscriptions created outside Checkout/Payment Links,
  no one-off invoices outside the billing period, no third-party tax integrations). Review the terms and fees in the Dashboard (Settings → Managed Payments) before going live and decide to keep it
  or turn it off (then you are the merchant of record and need Stripe Tax and registrations: https://docs.stripe.com/billing/taxes/collect-taxes). This code works either way; with it on, every Product needs a tax code.
- **Tax codes.** Both sandbox products use `txcd_10103001` (SaaS, business use), which fits an agency/freelancer audience. Use the personal-use code (`txcd_10103000`) if you sell mainly to consumers.
- **Stripe Invoicing.** Subscription invoices (and receipts, hosted invoice pages) are created by Stripe Billing automatically; configure branding and emails in the Dashboard.
  Ad-hoc invoices for individual agencies are not part of this application.

## Sandbox state (created for development)

Products "HawkEye Pro" ($19/month) and "HawkEye Agency" ($59/month) with lookup keys `hawkeye_pro_monthly` / `hawkeye_agency_monthly`, tax code `txcd_10103001`. Their price IDs are in your local `.env`.
Stripe's guidance is to use a dedicated sandbox per environment rather than the account's shared test mode; consider creating one for CI.

## Setup (test mode first)

1. **Keys.** Dashboard (test mode) → Developers → API keys: copy the secret key (`sk_test_…`). Put it in `.env` as `STRIPE_SECRET_KEY`.
   Live keys (`sk_live_…`/`rk_live_…`) are refused at startup unless `APP_ENV=production`.
2. **Products & prices.** Create two recurring monthly prices (Pro $19, Agency $59, or whatever you decide) and copy their IDs
   (`price_…`) into `STRIPE_PRICE_PRO_ID` and `STRIPE_PRICE_AGENCY_ID`. The *plan limits* live in our `plans` table; the Stripe price only decides
   what is charged. A price that is not one of the configured IDs never grants a plan.
   **Yearly billing (optional).** Create a recurring price billed **every 1 year** for each plan, at the plans table's `annual_price_cents` ($190 Pro,
   $590 Agency: two months free), and put their IDs in `HAWKEYE_PRO_ANNUAL_SUBSCRIPTION` and `HAWKEYE_AGENCY_ANNUAL_SUBSCRIPTION` (both or neither; all four
   price IDs must differ). With both set, `/plans` and `/billing/subscription` report `annual_billing_enabled: true` and the pricing cards offer a Monthly/Yearly
   choice; without them only monthly billing is offered and a yearly checkout is refused.
3. **Customer portal.** Dashboard → Settings → Billing → Customer portal. Test mode and live mode have separate configurations; set both. The portal
   session API needs a saved configuration. The settings this app and the refund policy (Terms 7) expect (live mode was checked against this on 2026-11-02, when
   only the two Annual products were listed for switching; add the monthly ones if that is still so):

   | Setting | Value | Why |
   |---|---|---|
   | Payment methods: customers can update | on | |
   | Cancellations: customers can cancel | on, **at the end of the billing period**, no proration or refund | access continues to the end of the paid term; no prorated refunds |
   | Subscriptions: customers can switch plans | on, with **all four** products: HawkEye Pro and Agency (monthly) and the two Annual products | the portal only offers a change when the customer's current price is in this list |
   | End trials on subscription updates | on (default) | no trials are offered, so it never applies |
   | When customers change plans or quantities | **Prorate charges and credits**, invoiced immediately (`always_invoice`) | an upgrade is paid for at once, with unused time on the old plan counted toward it; a credit is never paid out |
   | Downgrades: switching to a cheaper plan | **Wait until the end of the billing period** | no credit for unused time on the dearer plan |
   | Downgrades: switching to a shorter interval | **Wait until the end of the billing period** | yearly → monthly happens at renewal, never as a credit for unused months |

   A switch arrives as a normal `customer.subscription.updated` event; the app re-reads the subscription and records the new plan and interval. A deferred downgrade
   keeps the current price until the period ends, so the customer keeps the current plan in HawkEye until then.

   Also in Settings → Billing → Subscriptions and emails: **payment-update links go to a Stripe-hosted page** (the links in Stripe's failed-payment, expiring-card
   and renewal emails then work without signing in to HawkEye), and **upcoming-renewal reminders** are on (see *Refunds* below).

   **Check it** (read-only): create a restricted key with *Read* on Prices, Products, Customer portal and Webhook endpoints, then run a script that lists the active
   recurring prices, `GET /v1/billing_portal/configurations` (expand `features.subscription_update.products`; recent API versions omit the product list otherwise)
   and `GET /v1/webhook_endpoints`. Delete the key afterwards.
4. **Webhooks, local development.** Install the Stripe CLI, `stripe login`, then
   `stripe listen --forward-to localhost:8080/api/v1/webhooks/stripe`. It prints `Ready! Your webhook signing secret is 'whsec_…'`; put that in
   `STRIPE_WEBHOOK_SECRET` and restart the API and worker. (The CLI's secret differs from a Dashboard endpoint's.)
5. **Webhooks, deployed.** Workbench → Webhooks → create an event destination for *your account*, payload format **Snapshot** (this code reads snapshot
   events, not thin events), URL `https://<your-domain>/api/v1/webhooks/stripe`, and subscribe to only:
   `customer.subscription.created`, `customer.subscription.updated`, `customer.subscription.deleted`, `checkout.session.completed`, and the risk events
   `charge.dispute.created`, `charge.refunded`, `radar.early_fraud_warning.created` (surfaced for human review, see below).
   **`customer.subscription.updated` is essential**: without it plan switches, renewals, failed payments and portal cancellations never reach the app (the live
   endpoint was missing it until 2026-11-02). To change the events later: Workbench → Webhooks → the destination → *Edit destination* → *Select events*. Other
   events are acknowledged and ignored.
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
Browser ──POST /billing/checkout-session {plan, interval?}──▶ API  (owner + verified email; price from config; creates + stores the Stripe customer first; nothing granted)
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

**Billing interval.** Each subscription row records `billing_interval` (`month` or `year`) from the price Stripe reports, so the billing page can say "Billed yearly"
and a switch made in the customer portal shows up with the next event. Entitlements do not depend on it: a yearly Pro subscription gets exactly the Pro plan.

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
Canceling from the app (or the portal) sets *cancel at period end*: the owner keeps paid access until the period ends, then Stripe deletes the subscription and access ends.

## Price check

The site shows each plan's price from the `plans` table, while Stripe charges whatever the Stripe price says. Nothing ties the two together except this check: at API startup and
then every hour, `Service.VerifyPrices` reads `STRIPE_PRICE_PRO_ID` and `STRIPE_PRICE_AGENCY_ID` from Stripe and compares each with its plan: the amount (cents) must equal
`plans.monthly_price_cents`, the currency must be USD, the price must be recurring **monthly** (every 1 month) and **active**. When yearly prices are configured, each is checked
the same way against `plans.annual_price_cents` and must recur **every 1 year**; a yearly price for a plan with no `annual_price_cents` is a mismatch too.

- **On a mismatch** for a plan: checkout for **that plan at that interval** is refused (`503 PLAN_UNAVAILABLE`, so nobody is charged something other than what they were shown), the gauge
  `hawkeye_billing_price_mismatch{plan}` is 1 (`plan` is `pro`, `agency`, `pro_annual` or `agency_annual`) (alert `HawkEyeBillingPriceMismatch`), an ERROR is logged with the reason ("the pro plan is advertised at $19.00 a month but Stripe
  charges $29.00") and the error tracker is told once. The other plans and intervals stay open. It clears by itself at the next check after you fix it: either correct the Stripe price (create a new
  recurring price with the right amount, then put its `price_...` id in `STRIPE_PRICE_*_ID` and restart the API and worker) or correct the plan row.
- **If Stripe cannot be reached** the check proves nothing: it neither raises nor clears a problem, and checkout is not blocked by it.
- **Not checked:** existing subscriptions keep the price they were sold at. Changing a plan's price is a business decision: create a new Stripe price, update `plans.monthly_price_cents`
  (or `annual_price_cents`) and the env var together, and tell existing customers.
- **Run it by hand** after creating or editing prices (read-only; works with live or test keys; needs a test database):
  `STRIPE_LIVE_PRICE_CHECK=1 STRIPE_SECRET_KEY=... STRIPE_PRICE_PRO_ID=... STRIPE_PRICE_AGENCY_ID=... TEST_DATABASE_URL=... go test ./internal/billing -run LivePrices -v`
  (add `HAWKEYE_PRO_ANNUAL_SUBSCRIPTION=... HAWKEYE_AGENCY_ANNUAL_SUBSCRIPTION=...` to check the yearly prices too)
- Stripe Tax is not enabled by this app (no `automatic_tax` on Checkout). If you turn it on, set a tax behavior on each price: the live prices currently have it "unspecified".

## Risk events: disputes, refunds, fraud warnings

`charge.dispute.created`, `charge.refunded` and `radar.early_fraud_warning.created` are stored, logged at WARN with the Stripe object IDs, and counted in
`hawkeye_billing_risk_events_total{type}` (alert on any increase). They **never change entitlements automatically**: whether a refund or dispute should end access is a business decision.
Manual runbook: open the object in the Stripe Dashboard, identify the subscription (via the charge → invoice → subscription, or the customer ID in the log), decide, then act in Stripe
(cancel the subscription, which flows back as a verified event) or leave it. Not automated yet: resolving the affected subscription from these events (Stripe's object graph differs by API version).

## Refunds (the policy in the Terms, section 7.C)

The policy: monthly payments are generally non-refundable; a **first** annual purchase can be refunded in full on request within **14 calendar days**; an annual
**renewal** can be refunded on request within **7 calendar days** of the charge when the paid service has not been materially used since; duplicate or wrong charges are
always corrected. Requests arrive at `customer_support@hawkeye.observer` (contact form topic "Billing or subscription"). Nothing is automated: you decide each request in the
Stripe Dashboard.

**Granting a refund that ends the subscription** (the 14-day guarantee, an unwanted renewal):

1. Stripe Dashboard → Customers → the customer (its ID is in the contact-form message for a signed-in sender, or search by email) → the subscription.
2. Check eligibility: for the guarantee, it is the organization's **first** annual purchase and today is within 14 days of it; for a renewal, within 7 days of the renewal
   charge. For "no material use", look at the organization's monitors and activity in the app.
3. **Cancel the subscription immediately** (Actions → Cancel subscription → *Immediately*, not at period end) and choose to refund the last payment in full in the same dialog.
   If you refunded the payment first, cancel immediately afterwards.
4. Stripe sends `customer.subscription.deleted` and `charge.refunded`. The first returns the organization to the Free plan (nothing is deleted); the second is logged as a risk
   event and changes nothing by itself.
5. Reply to the customer from `customer_support@hawkeye.observer`.

A refund **without** canceling (a duplicate charge, a goodwill credit) leaves the plan as it is: `charge.refunded` never changes entitlements, by design.

**Renewal reminders.** The Terms promise the renewal reminders the law requires, and the 7-day renewal refund assumes customers had a chance to cancel. Turn on Stripe's
upcoming-renewal emails for annual subscriptions: Dashboard → Settings → Billing → Subscriptions and emails → *Send emails about upcoming renewals*, **30 days**
before renewal. Some US states (California, for example) require notice of a yearly auto-renewal well before the charge; check the rules where you sell. Also turn on
refund receipts (Settings → Customer emails → *Refunds*) so customers get a confirmation when you grant one.

## Safety rules enforced in code

- **Prices come from configuration only**; the checkout endpoint reads just the plan name (`pro`/`agency`) and the interval (`month`, the default, or `year`). Tested: client-supplied `price`/`amount` fields are ignored.
- **Only owners** can start checkout, open the portal, cancel or re-sync, and the account email must be **verified** (members can view the plan).
- **Customer first.** The Stripe customer is created (idempotency key per organization) and its ID stored *before* Checkout, so later events are attributed through the customer ID,
  with checkout/subscription metadata only as a fallback.
- **One subscription per organization.** A second paid subscription is reported as a conflict (event retried then `failed`) rather than overwriting the first; a stray non-paying
  duplicate being canceled is ignored. Choosing a plan while subscribed returns `ALREADY_SUBSCRIBED` (use the portal). One Stripe customer maps to exactly one organization (unique index).
- **Unknown prices never grant access.** A subscription whose price isn't one of the configured monthly or yearly price IDs is rejected (and a cancellation of one is still honored).
- **Webhook endpoint**: signature checked on the raw body before anything is stored (5-minute replay tolerance); invalid → 400 and nothing stored; valid → stored durably
  and acknowledged; duplicates acknowledged without a second effect; no session or CSRF token needed (registered outside the CSRF-protected group). Body limit 1 MB.
- Provider errors are logged in full and returned to users as a generic `BILLING_PROVIDER_ERROR`; secrets are never logged. Test keys only outside production.

## Operations

- Deleting an account first deletes the Stripe customer (which cancels its subscriptions; verified against the sandbox), then the data. If Stripe is unreachable the deletion fails and can be retried.
- `POST /api/v1/billing/sync` (owner) re-reads the organization's subscription from Stripe and applies it: the repair tool when webhooks were missed (e.g. an outage longer than Stripe's 3-day retry window).
- Stripe's Workbench → Webhooks → *Event deliveries* shows delivery status and can resend events (15 days via Dashboard, 30 days via CLI `stripe events resend`).
- Metrics, alerts and SQL for stuck events: `docs/operations.md` (Billing).
- Stored webhook payloads (which include customer details) are deleted after 90 days (failed ones after 180).

## Keys

- The sandbox secret key lives only in your local `.env` (gitignored, mode 600). It was shared in a chat session while setting this up: **roll it** in the Dashboard (Developers → API keys) when convenient and prefer a
  **restricted key** (`rk_test_…`) with only the permissions this app uses (Customers, Checkout Sessions, Customer portal sessions, Subscriptions: write; Prices/Products: read). Test it by running the live smoke tests
  and adding any permission a `403` reports. Use separate keys per environment and never put a live key in a file that is committed.
- A publishable key (`pk_…`) is not used: we use Stripe-hosted Checkout, so no Stripe.js and no card data touch our pages (a Content-Security-Policy header is still a Phase 8 item).
- Stripe SDK: `stripe-go/v87` (current as of this writing).

## Running the live smoke tests

```sh
set -a; . ./.env; set +a
export TEST_DATABASE_URL='postgres://postgres@localhost:5432/monitoring_test?sslmode=disable'
STRIPE_LIVE_SMOKE=1 go test -count=1 -run LiveStripe -v ./internal/billing
```
They refuse to run with a non-test key, create one customer and one subscription per run and delete them at the end.
