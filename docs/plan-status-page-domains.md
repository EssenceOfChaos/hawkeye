# Plan: status pages on your own domain

Status: **built** (2026-11-15). The four decisions below were made as recommended. Customer guide: `/docs/status-page-domains`; operations:
`docs/operations.md` (*Status pages on customers' own domains*); security: `docs/security.md`.

**Launch checklist.** (1) `domains.hawkeye.observer` exists: a CNAME to the apex, DNS only (added in the Cloudflare dashboard on 2026-11-15, as
production DNS is not under Terraform state yet). (2) Before deploying, email the confirmed `status_page_domains` waitlist sign-ups (query in
`docs/operations.md`, *Feature waitlists*): migration `000045` deletes them. (3) After deploying, `scripts/smoke.sh` and one real domain end to end. Waitlist key `status_page_domains` (`web/src/comingSoon.ts`); retire it at launch, as migration 000039 did for
API workflows.

A customer points a host name they own, such as `status.clientbrand.com`, at HawkEye. Once HawkEye has confirmed they control it, that status
page is served there over HTTPS with a certificate HawkEye obtains and renews. The page at `/status/<slug>` keeps working.

## Decisions for you

| Decision | Recommendation |
|---|---|
| Which plans | **Agency only**, one domain per status page (so up to 50). The roadmap's reason for the feature is "a premium reason for agencies to choose Agency". Pro sees it on the pricing card as an Agency feature |
| Apex domains (`clientbrand.com` itself) | **Not at launch.** An apex can't have a CNAME, so it would need an A record holding our server's IP, which breaks silently whenever the server is rebuilt (`docs/rebuild.md`). Subdomains only; the form explains why |
| The `/status/<slug>` address once a domain is live | **Keeps working, and a per-page setting can redirect it to the domain** (off by default). The page's canonical tag points to the domain whatever the setting |
| The DNS target customers point at | A dedicated name, **`domains.hawkeye.observer`** (a CNAME to the apex, not proxied; `infra/main.tf`), so a new server IP changes nothing for customers |

## How it works for the customer

1. On a status page's settings, **Custom domain** → enter `status.clientbrand.com`.
2. HawkEye shows two DNS records to add:
   - `CNAME status.clientbrand.com → domains.hawkeye.observer` (routes visitors to us)
   - `TXT _hawkeye.status.clientbrand.com → hawkeye-verify=<random token>` (proves they control the name; the CNAME alone doesn't, see *Security*)
3. HawkEye checks every minute for the first hour, then every 15 minutes for 3 days, and the **Check now** button checks at once. When both
   records are right, the domain is **Active**, the owner gets an email, and the first visit obtains the certificate (a few seconds).
4. If either record later disappears or changes, the domain goes back to **Needs attention** with the reason. After 7 days in that state it stops
   being served, and the owner gets an email the first time and when it stops.

States: `pending` (records not seen yet), `active`, `failing` (was active, now a record is wrong; still served for 7 days), `disabled`
(stopped; the owner can check again or remove it). Verification gives up after 3 days in `pending` and stays there until **Check now**.

## Design

### Data model (migration `000044_status_page_domains`)

- `status_page_domains`: `id`, `page_id` (unique; with `organization_id`, a composite key to the page, `ON DELETE CASCADE`), `hostname`
  (lower-case ASCII/punycode, below a registered domain), `verify_token`, `state`, `last_error`, `last_checked_at`, `next_check_at`,
  `verify_started_at`, `verified_at`, `failing_since`. The token is stored as it is: it is published in the customer's own DNS, so it is not a
  secret, and knowing it doesn't help anyone who can't write to that zone.
- A host name is unique **only once verified** (a partial unique index on `active` and `failing`), and once per organization. So anyone can claim a
  pending name, but a squatter who claims a client's name first blocks nobody: only the claim whose token is in the zone verifies.
- `status_pages.redirect_to_domain boolean NOT NULL DEFAULT false`.
- `plans.max_status_page_domains integer NOT NULL DEFAULT 0` (Agency: 50).
- Audit events (target type `status_page_domain`): a domain added, verified, removed and disabled.

### Verification (`internal/statuspagedomains`)

- Host names go through `publichost` plus extra rules: no IPs, at least three labels, not under `hawkeye.observer` or `APP_BASE_URL`'s host, and
  not a public suffix (`golang.org/x/net/publicsuffix`).
- A job in the production worker, next to the domain-expiry job (`domains.NewJob` in `cmd/worker/main.go`), runs due checks; regional workers
  (`WORKER_CHECKS_ONLY`) never run it. **Check now** runs the same check from the API at once, rate-limited per page. TXT: look up
  `_hawkeye.<host>` and compare in constant time. CNAME: follow the chain and require it to end at `domains.hawkeye.observer`, *or* accept that
  the name resolves to the same addresses (for DNS hosts such as Cloudflare that flatten CNAMEs).
- Lookups use the same resolver as DNS monitors, with a 5-second timeout each. A lookup that gets no answer (a timeout) never counts against
  an active domain.

### Serving (Caddy)

- Global `on_demand_tls { ask http://api:8080/internal/tls/allow }`. The API answers 200 only for a host in state `active` or `failing`, and
  that path is **not** in Caddy's `@backend` list, so it can't be reached from outside. Without `ask`, anyone could point names at us and use up
  certificate rate limits; with it, only verified names get certificates.
- A second site block, `https:// { tls { on_demand } }`, for every other host. It serves **only** the status page:
  - `/` → a small separate build entry, `status.html` (the status page component and nothing else: no sign-in, no app routes, no analytics),
  - `/api/v1/public/domain-status` and `/api/v1/public/domain-status/logo` → the API, which finds the page from the `Host` header,
  - `/assets/*` → the hashed assets that entry needs,
  - anything else → 404.
- On those hosts: HSTS **without** `includeSubDomains` (we must not set policy for the customer's other subdomains), a CSP with no third-party
  origins, and `X-Robots-Tag` following the page's own setting.
- The certificates live in the `caddy_data` volume, which is already backed up with the host; `docs/rebuild.md` gets a note that a rebuild re-issues
  them on first visit.

### API

- `PUT /api/v1/status-pages/:pageID/domain` `{hostname}` (owner, Agency) → the records to add; `POST …/domain/check`; `DELETE …/domain`.
- `GET /api/v1/status-pages/:pageID` gains `domain: {hostname, state, records, last_error, last_checked_at}`.
- The public status response gains `canonical_url`. MCP `list_status_pages` shows the domain and its state.

### App

- A **Custom domain** card on the status page editor: the records with copy buttons, the state, the last error in plain words ("The TXT record
  wasn't found. DNS changes can take up to an hour."), **Check now**, **Remove**, and the redirect switch.
- Pro and Free see the card with an "Agency plan" upgrade link, as the other Agency features do.

## Security

- **Domain takeover.** If a customer deletes the page but leaves their CNAME pointing at us, someone else could claim that name on HawkEye. The
  per-claim TXT token prevents it: a new claimant can't create a TXT record in someone else's zone. Only one claim of a name can be verified at a
  time, and removing a domain frees it at once.
- **Certificate abuse.** `ask` gates issuance; it is answered from a cached lookup, and the endpoint is rate-limited so a flood of TLS handshakes
  for random names can't load the database.
- **Phishing on customer domains.** Nothing but the status page and its two public reads is served there: no sign-in form, no cookies, no app.
- **Re-checks.** Every active domain is checked again daily; a record that has gone stops being served after 7 days.
- Add the threat model to `docs/security.md` and a row to `docs/security-review.md`.

## Build order and estimate

| Step | Work | Days |
|---|---|---|
| 1 | Migration, `statuspagedomains` service, host name rules, DNS verification, unit and integration tests | 4 |
| 2 | Verification scheduler, state changes, emails, audit events | 2 |
| 3 | API endpoints, plan limit, MCP field, `docs/api.md` | 2 |
| 4 | Caddy `ask` endpoint and on-demand site block; separate `status.html` build entry; host-based public endpoints | 3 |
| 5 | Editor card and redirect setting; web tests | 2 |
| 6 | Smoke stack test with the private CA (`make smoke-up`), `docs/security.md`, operations runbook, the feature-sync sweep (pricing card, landing, docs page, llms.txt, README, legal), retire the waitlist and email its sign-ups | 3 |

About 3 weeks. Steps 1 and 2 can ship on their own: without the API endpoints (step 3) nobody can claim a domain, and nothing is served until
step 4.

**Done:**

- Step 1: `migrations/000044_status_page_domains`, `internal/statuspagedomains`, `plans.CanAddStatusPageDomain`.
- Step 2: `statuspagedomains.Job` on the primary worker (`STATUS_PAGE_DOMAIN_TARGET`, `DISABLE_STATUS_PAGE_DOMAIN_CHECKS`), metric
  `hawkeye_status_page_domain_checks_total{result}`, and security log events `status_page.domain_{set,removed,verified,failing,disabled}`. The
  emails (live, needs attention, working again, no longer served) go to the organization's owner directly, not through alert destinations: they
  are about the account's setup, not a monitored site. They are sent at most once; the editor shows the same state if one is lost.
- Step 3: `GET/PUT/DELETE /status-pages/:id/domain` and `POST …/domain/check` (`docs/api.md`, *Custom domains*), the status page's read-only
  `custom_domain` and `custom_domain_state` and its `redirect_to_domain` setting (a save that leaves it out keeps it), and the MCP fields. The
  redirect itself is done in step 4, with the serving side.
- Step 4: Caddy's `on_demand_tls` asks `GET /internal/tls/allow` (200 only for a verified domain of a published page on a plan that includes
  them; answers cached 30 s; refused when the request came through a proxy). A catch-all site (`STATUS_DOMAINS_ADDRESS`, `https://` in
  `docker-compose.prod.yml`) serves only `status.html` (`web/src/status-main.tsx`: the status view with no router, sign-in or analytics), the two
  `/api/v1/public/domain-status` reads (found from the `Host` header) and `/assets/*`; everything else is a 404, with a CSP of our own origin only
  and HSTS without `includeSubDomains`. `http://` on those names redirects to https. `/status/<slug>` names the domain as canonical, and
  redirects there when `redirect_to_domain` is on. After a downgrade the claim is kept but nothing is served or certified. Checked on the smoke
  stack (`CADDY_EXTRA_GLOBAL=local_certs`): certificate on first visit, nothing but the page on the domain, no certificate for an unverified
  name, the main site unchanged, and the page rendering in Chromium with no console errors, no cookies and no other host contacted.
- Step 5: the **Custom domain** card on a status page's settings (`web/src/components/CustomDomain.tsx`): the two DNS records with Copy buttons,
  the state and the problem in plain words, **Check now**, **Use another domain** and **Remove**; Free and Pro see it as an Agency upgrade.
  The redirect switch is in the page's own form, shown only when the page has a domain, and removing a domain turns it off. The list of pages
  shows each page's domain. Also fixed on the way: public status pages scrolled sideways on phones (the day tooltips at either end of the row
  now line up with that end), the tab read "Acme status status" for a page titled "Acme status", and the editor's branding section scrolled
  sideways on phones.
- Step 6: the waitlist retired (migration `000045`, `waitlist.Features`, the landing page's coming-soon card), the pricing card line (Agency),
  the landing page's status page card, FAQ and paid-plans summary, the customer guide `/docs/status-page-domains` (route, SEO entry, Caddy `@app`),
  `llms.txt`, README, the Privacy Policy and `docs/legal-placeholders.md`, and `docs/architecture.md`, `operations.md`, `deployment.md`,
  `security.md`, `security-review.md`. The DNS target is a CNAME to the apex (`infra/main.tf`), not A and AAAA records as first planned.

## Operations

- DNS: `domains.hawkeye.observer` is a CNAME to the apex, DNS only (`cloudflare_dns_record.status_domains` in `infra/main.tf`). Apply it
  before launch. Customers' CNAMEs then resolve through it to the server's reserved IP, which verification accepts (the address check).
- Metrics: `hawkeye_status_page_domains{state}` and `hawkeye_status_page_domain_checks_total{result}`; an alert when on-demand issuance fails
  repeatedly (Caddy logs `obtaining certificate` errors).
- Let's Encrypt limits are per registered domain (50 certificates a week), so one client can't affect another.

## Not in this plan

- Apex domains, wildcard domains, customer-supplied certificates, more than one domain per page, and custom domains for anything other than
  status pages (badges, reports).
- Status page subscribers (the other waitlist): it needs the Privacy Policy and counsel review first (`docs/legal-placeholders.md`).
