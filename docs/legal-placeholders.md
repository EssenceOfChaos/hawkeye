# Legal placeholders: what counsel must decide

> **Status (2026-10-14):** the **Privacy Policy** (`/privacy`) is now complete text, written from the owner's template and checked against how the system actually works; the draft banner was removed from that page. It has **not** had attorney review. The **Terms and Conditions** (`/terms`) are now complete text too (same date, same decisions), also without attorney review, and the draft banner was removed from both pages. Decisions recorded for the Privacy Policy: operated by "Hawkeye", a limited liability company (exact legal name and state of formation not stated; no mailing address by choice); contact `support@hawkeye.observer`; minimum age 18; offered to the US plus EU/UK; hosted in the United States (Vultr); no analytics, advertising or error-tracking vendor in use.
>
> **Keep the policy true:** it must be updated *before* any of these change: adding analytics or non-essential cookies, enabling Sentry (`SENTRY_DSN`), adding a sub-processor, changing retention windows (`internal/retention`), changing backup retention (the policy promises up to about 60 days), or hosting outside the US.
>
> **Terms: business decisions baked in, confirm they are what you want:** refunds (no refunds for partially used periods unless the law requires), liability cap (greater of 12 months' fees or US $100), Pennsylvania governing law with courts located in Pennsylvania (no county named, no arbitration or class-action waiver), prices $19/$59 per month in USD, monthly billing only, failed payments keep paid features during Stripe's retries and then revert to Free. Check that the Stripe prices really are in USD and that tax collection (Stripe Tax / Managed Payments) matches "taxes shown at checkout". Keep the Terms' plan list in step with the `plans` table and the pricing page.
>
> **Open items for counsel:** (1) whether an EU/UK representative (GDPR Art. 27) and/or a UK representative is required, since the Service is offered to EU/UK users from the US; (2) which US state privacy laws apply as the business grows; (3) the exact LLC name and state to show in Section 1; (4) data-processing agreements to offer business customers (the policy says one is available on request); (5) whether Gmail-as-support-mailbox and Cloudflare Email Routing need DPAs listed.

The app ships `/privacy` and `/terms` pages containing a prominent "PLACEHOLDER — DRAFT FOR LEGAL REVIEW" banner. **They are not legal advice and must be replaced before launch.** This file lists the facts the documents must reflect.

## Personal data inventory

| Data | Why | Retention |
|---|---|---|
| Email, password hash (Argon2id), name | account | until account deletion |
| Session records (hashed token, expiry; no IP or user agent stored) | sign-in | until expiry/sign-out |
| Monitor URLs/names, check results (status, latency, error class) | the service | per plan: 7 / 90 / 365 days; incidents kept |
| Alert recipient emails | alerts | until removed or account deleted |
| Billing: Stripe customer ID, subscription state | billing | until account deletion; Stripe keeps its own records (tax/accounting law) |
| Webhook event payload copies | idempotency/debugging | 90/180 days |
| Rate-limit counters (hashed keys) | abuse protection | 24 h |
| Contact-form messages (name, email, message, optional website, account id/plan if signed in) | support | live in the support mailbox, not the database; decide a deletion period |
| Server logs (request ids, no bodies; tokens redacted) | operations | set by your log retention |

Sub-processors to list: hosting provider, database provider, SMTP provider, Stripe, Sentry (if `SENTRY_DSN` set; events are scrubbed of emails/tokens), uptime monitor.

## Decisions for counsel

* Controller/processor roles (customers monitor their own sites, but alert-recipient emails may belong to third parties).
* Lawful bases, DPA for business customers, international transfers, cookie notice (the app sets only a strictly necessary session cookie and CSRF cookie).
* Self-service export (`GET /api/v1/account/export`) and deletion (`POST /api/v1/account/delete`) exist; confirm they satisfy access/erasure obligations, including the shared-organisation case (owners must transfer or delete first) and backups (deleted data persists in backups until they expire: state the period).
* Terms: acceptable use (monitoring only sites you are authorised to test), SLA/no-SLA, liability, refunds/disputes (handled manually today), governing law, trademark check of the "HawkEye" name and logo.
* Stripe Managed Payments / tax registration.
