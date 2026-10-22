# Legal placeholders: what counsel must decide

> **Status (2026-10-14):** the **Privacy Policy** (`/privacy`) is now complete text, written from the owner's template and checked against how the system actually works; the draft banner was removed from that page. It has **not** had attorney review. The **Terms and Conditions** (`/terms`) are now complete text too (same date, same decisions), also without attorney review, and the draft banner was removed from both pages. Decisions recorded for the Privacy Policy: operated by "Hawkeye", a limited liability company (exact legal name and state of formation not stated; no mailing address by choice); contact `support@hawkeye.observer`; minimum age 18; offered to the US plus EU/UK; hosted in the United States (Vultr); Google Analytics (GA4, measurement ID G-ZNT8EMTFGY) added 2026-10-20, on by default with an opt-out ("Cookie settings" link and Global Privacy Control), because the service is marketed to US customers; must become opt-in if EU/UK customers appear, and disclosed in the Privacy Policy (sections C and E); no advertising vendor in use. Error tracking (Sentry) is optional and off unless `SENTRY_DSN` is set; the Privacy Policy discloses it (section 4.A) so it can be switched on, but the wording describes what `internal/errtrack` really sends (scrubbed message, release, component tags; no user, request or server name), so keep the two in step.
>
> **API checks (added after the first version of these pages):** the Privacy Policy and Terms now describe request settings and stored credentials (encrypted, write-only, wiped on monitor deletion, excluded from exports), the state-changing-request rules (verified email, 5-minute minimum, customer responsibility), and that response bodies are read up to 1 MB to evaluate checks but never stored. Keep them in step with `docs/security.md`.
>
> **SSL certificate expiry (added later):** the Privacy Policy now lists the stored certificate expiry date (not the certificate), and the Terms describe the warning emails (30, 14, 7, 1 days and on expiry) as a courtesy with no guarantee. Keep both in step with `checks.SSLBuckets`.
>
> **Domain expiry (added later):** the Privacy Policy now lists the stored domain names, their registry-reported expiry dates and lookup times, and says that lookups send only the domain name (and our server's address) to the public registry and IANA. The Terms describe the warnings (30, 14, 7, 1 days and on expiry) as a courtesy, and say that registries may not share or may hide expiry dates ("Expiry unavailable" is not a statement that a domain is safe). Keep both in step with `domains.Bucket` (which reuses `checks.SSLBuckets`).
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
| Slack / webhook destination URLs and webhook signing secrets (stored encrypted; only the host is ever displayed) | alerts | until removed or account deleted |
| Maintenance windows (name, schedule, covered monitors) | suppress alerts during planned work | until removed or account deleted |
| Status page (address, title, description, chosen monitor names; **public once published**) | customer-facing status | until unpublished/deleted or account deleted; cached under a minute |
| Billing: Stripe customer ID, subscription state | billing | until account deletion; Stripe keeps its own records (tax/accounting law) |
| Webhook event payload copies | idempotency/debugging | 90/180 days |
| Rate-limit counters (hashed keys) | abuse protection | 24 h |
| Contact-form messages (name, email, message, optional website, account id/plan if signed in) | support | live in the support mailbox, not the database; decide a deletion period |
| Server logs (request ids, no bodies; tokens redacted) | operations | set by your log retention |

Alert content (monitor name and address, event time, short failure description) is also sent to customer-chosen Slack and webhook endpoints; Slack is a third party the customer chooses, not our sub-processor.

Sub-processors to list: hosting provider, database provider, SMTP provider, Stripe, Sentry (if `SENTRY_DSN` set; events are scrubbed of emails/tokens), uptime monitor.

## Decisions for counsel

* Controller/processor roles (customers monitor their own sites, but alert-recipient emails may belong to third parties).
* Lawful bases, DPA for business customers, international transfers, cookie notice (the app sets only a strictly necessary session cookie and CSRF cookie).
* Self-service export (`GET /api/v1/account/export`) and deletion (`POST /api/v1/account/delete`) exist; confirm they satisfy access/erasure obligations, including the shared-organisation case (owners must transfer or delete first) and backups (deleted data persists in backups until they expire: state the period).
* Terms: acceptable use (monitoring only sites you are authorised to test), SLA/no-SLA, liability, refunds/disputes (handled manually today), governing law, trademark check of the "HawkEye" name and logo.
* Stripe Managed Payments / tax registration.
