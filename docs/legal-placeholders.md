# Legal placeholders: what counsel must decide

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
| Server logs (request ids, no bodies; tokens redacted) | operations | set by your log retention |

Sub-processors to list: hosting provider, database provider, SMTP provider, Stripe, Sentry (if `SENTRY_DSN` set; events are scrubbed of emails/tokens), uptime monitor.

## Decisions for counsel

* Controller/processor roles (customers monitor their own sites, but alert-recipient emails may belong to third parties).
* Lawful bases, DPA for business customers, international transfers, cookie notice (the app sets only a strictly necessary session cookie and CSRF cookie).
* Self-service export (`GET /api/v1/account/export`) and deletion (`POST /api/v1/account/delete`) exist; confirm they satisfy access/erasure obligations, including the shared-organisation case (owners must transfer or delete first) and backups (deleted data persists in backups until they expire: state the period).
* Terms: acceptable use (monitoring only sites you are authorised to test), SLA/no-SLA, liability, refunds/disputes (handled manually today), governing law, trademark check of the "HawkEye" name and logo.
* Stripe Managed Payments / tax registration.
