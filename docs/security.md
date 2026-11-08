# Security notes

Living document; extended each phase.

## Authentication (Phase 2)

- **Passwords:** Argon2id (64 MiB, t=3, p=2), PHC-encoded so parameters can be raised later. Max 128 chars bounds per-request cost.
- **Login timing:** unknown accounts verify against a dummy hash; unknown, disabled and wrong-password cases return the same error.
- **Tokens** (sessions, reset, verification): 256-bit random, stored only as SHA-256 hashes, single-use where applicable, with expiry. Reset/verification consumption is one atomic `UPDATE … RETURNING`, so concurrent redemption cannot succeed twice.
- **Reset:** newest link only, revokes all sessions, response never reveals whether an account exists.
- **Cookies/CSRF:** HTTP-only, SameSite=Lax, Secure in production, plus double-submit CSRF token on unsafe methods.
- **Rate limiting:** per-IP on register/login/forgot/reset/verify/resend. `X-Forwarded-For` is ignored unless `TRUST_FORWARDED_FOR=true` (set only behind a trusted proxy), otherwise clients could spoof their IP.
- **No per-account lockout** yet (it would enable lockout abuse); IP limits only.

## Tenant isolation

Every organization query joins `organization_members` on the caller's user ID, so a non-member gets 404 identical to a missing
organization. Malformed IDs also return 404. `TestOrganizationIsolation` covers read, members, update and listing across tenants.
Monitors are always queried by `(id, organization_id)`, with the organization resolved from verified membership (`X-Organization-ID` is checked, never trusted). `TestMonitorTenantIsolationOverHTTP` and `TestTenantScoping` cover cross-tenant get/patch/pause/delete/list. Client-supplied state fields (`current_status`, counters, `organization_id`) are ignored (`TestClientCannotSetInternalState`).

## Plan limits

Limits are enforced in the service layer inside the same transaction as the write, under an organization row lock; the UI only mirrors them.

## Billing (Phase 7)

- **Entitlements change only from the payment provider's state**, read from Stripe after a *signed* webhook (or an explicit owner-triggered sync). A Checkout redirect, a created session
  or a client-supplied value never grants a plan; prices and the organization are chosen server-side (`TestCheckoutSessionUsesServerSidePrices`, `TestUpgradeAndDowngradeFlow`).
- **Webhook endpoint:** HMAC-SHA256 signature over the raw body, 5-minute replay window (Stripe's library), constant-time comparison (library), nothing stored before verification, 1 MB body limit,
  idempotent by event ID. Defense in depth (documented, infrastructure): allowlist Stripe's IPs. It is intentionally outside CSRF/session protection because it is signature-authenticated.
- **Authorization:** only owners with a verified email can start checkout, open the portal, cancel or re-sync; every call is scoped to the caller's verified organization (tested across tenants).
- **Secrets:** (prefer a restricted `rk_` key; see `docs/billing.md` → Keys) `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` come from the environment only, are validated at startup (shape, all-or-nothing, live keys refused outside production) and never logged. Provider error text is not returned to users.
- **No card data** is handled or stored; Checkout and the portal are Stripe-hosted. Stored webhook payloads (customer IDs/emails) are retained 90 days (180 for failed).
- **Residual risks:** a compromised webhook secret lets an attacker trigger *re-reads* but not choose the applied state (Stripe's API decides it), which limits the blast radius; the conflicting-second-subscription case requires manual resolution;
  dispute/refund/fraud-warning events are surfaced (log + alert metric) but deliberately not acted on automatically: define the policy before live launch (`docs/billing.md`).

## Data retention and privacy (Phase 6)

- Stored per check: status, timings and a fixed sanitized error summary — never page content. Retention windows per plan: Free 7 days, Pro 90, Agency 365,
  applied by an hourly batched job (see `docs/architecture.md`). Monitors you delete are purged after 30 days; expired sessions and one-time tokens are removed.
- Report endpoints are bounded (≤ 366-day windows, ≤ 150 chart points, ≤ 200 history rows per page) and tenant-scoped (`TestReportEndpointsAreTenantScoped`).
- **Not yet implemented:** self-service account deletion/export (needed for GDPR-style requests) and a published privacy policy; both are Phase 8 items.

## Alerts and email abuse (Phase 5)

- An alert address other than the owner's own must be confirmed by an emailed single-use link (48 h, hashed in the database) before it receives
  anything, and adding one requires a verified account email and is rate-limited — so the product cannot be used to spam third parties.
- Maximum 10 recipients per organization; only owners change recipients (members can view).
- Email subjects/bodies are plain text built from monitor names and URLs with control characters stripped, so a malicious monitor name cannot inject headers or extra lines (`TestRenderCannotInjectHeadersOrLines`).
- Stored delivery errors are truncated and sanitized; check results never contain response bodies.
- **Not yet implemented:** one-click unsubscribe links in alert emails (recipients can only be removed by an owner), and bounce/complaint handling (needs a real email provider).

## SSRF controls (Phase 4)

Users submit URLs that our servers fetch, so the check engine is an SSRF risk. The control is a dedicated client,
`internal/safehttp`, used for every check; URL parsing alone is never relied on.

| Threat | Control | Test |
|---|---|---|
| Private/loopback/link-local/metadata/reserved targets (IPv4 and IPv6) | `IsPublicIP` allow-list logic plus explicit blocked prefixes: RFC 1918, 127/8, `::1`, 169.254/16 (cloud metadata), 100.64/10 (incl. Alibaba metadata), `fc00::/7` (incl. AWS IPv6 metadata), multicast, reserved/broadcast, documentation and benchmarking ranges | `TestIsPublicIP` |
| IPv4 hidden in IPv6 | IPv4-mapped addresses are unmapped and judged as IPv4; NAT64 (`64:ff9b::/96`), 6to4 (`2002::/16`), Teredo, and IPv4-compatible (`::/96`) are blocked outright | `TestIsPublicIP` |
| Hostnames that resolve internally | We resolve the name ourselves; **every** answer must be public (one private answer blocks the host, so mixed records cannot be used) | `TestHostnameResolving…`, `TestMixedAnswersAreBlocked` |
| DNS rebinding | Resolution happens once per connection and we dial the **validated IP literal**, never the hostname, so a later different DNS answer cannot be used. Connections are not reused (`DisableKeepAlives`) | `TestDNSRebindingCannotChangeTheDialedAddress` |
| Defense in depth at connect time | The default dialer's `Control` hook re-validates the actual remote address of every connection | `TestDefaultDialerControl…` |
| Redirects to internal targets | Every redirect hop dials through the same validator; redirect scheme must be http/https, no credentials, max 5 hops | `TestRedirectToInternalTargetIsBlocked`, `TestRedirectLimitAndSchemes` |
| Alternate IP spellings (`2130706433`, `0x7f.1`, `127.1`, octal) | They are treated as host names, resolved, and the resulting address is validated; also rejected earlier by monitor URL validation | `TestAlternateIPSpellingsNeverConnect` (asserts a local listener sees zero connections) |
| Port abuse (SSH, databases, internal services) | Only ports 80 and 443, checked at monitor creation, at dial time and on redirects | `TestPortsOtherThan80And443AreBlocked` |
| Proxy bypass | The transport ignores `HTTP(S)_PROXY`; a proxy would dial for us | `TestEnvironmentProxyIsIgnored` |
| Data exfiltration through responses | Bodies are never stored or interpreted (at most 64 KiB is drained); only status, timing and a fixed error summary are persisted. Raw network errors (which can contain internal IPs) are never stored | `TestProhibitedDestinationsUnderProductionPolicy` |
| Resource exhaustion | Hard per-check timeout (1–30 s, covers DNS, connect, TLS, headers and redirects), 64 KiB response header limit, bounded worker concurrency | `TestTimeout` |

Mutation checks performed while developing: removing the pre-dial validation fails six tests; honoring proxy
environment variables fails `TestEnvironmentProxyIsIgnored`.

**Infrastructure layer (required in production, not enforceable from application code):** run workers in a network
that cannot reach internal services. Block egress from the worker hosts/containers to RFC 1918 ranges, link-local
`169.254.0.0/16` and `fd00::/8`, the database and other internal services except what the worker itself needs
(PostgreSQL on its port), and enforce IMDSv2 / disable instance metadata on worker hosts. Details in `docs/deployment.md` (Phase 8).

**Residual risks:** DNS answers are validated at dial time, so a hostname's content can still change between checks
(that is inherent to monitoring a third-party site, not an SSRF); the checker trusts the system resolver and its
TLS roots; a malicious site can make us send a GET/HEAD with our fixed `User-Agent` to any *public* address, which is the product's purpose.

## Production hardening (Phase 8)

- **Rate limits:** a shared fixed-window limiter in PostgreSQL (`rate_limits`, keys are hashed, rows purged after 24 h) so limits hold across API replicas. Per-IP on auth routes, a per-account login ceiling (30/h: bounds
  password guessing across many IPs; the trade-off is that an attacker who knows an email can lock that account's *logins* for up to an hour, accepted because sessions already signed in keep working and reset still works),
  per-address hourly budgets on emails we send (verification, reset, alert confirmation; stops mail-bombing a victim), and an in-memory global per-IP ceiling. If the limiter's database fails it **fails open** and counts `hawkeye_rate_limiter_errors_total` (availability over strictness; alerted).
- **Headers:** CSP `default-src 'self'; script-src 'self' https://*.googletagmanager.com https://bzrcdn.openai.com` (no inline scripts; besides the OpenAI Ads pixel's hosts below, the only third-party origins are the ones Google documents for Google Analytics and Google Ads conversion measurement in `script-src`, `connect-src` and `img-src`: `*.googletagmanager.com`, `*.google-analytics.com`, `analytics.google.com` and `*.analytics.google.com`, `*.googleadservices.com`, `*.g.doubleclick.net`, `ad.doubleclick.net`, `pagead2.googlesyndication.com`, `*.google.com`, `*.google.co.uk`, `*.google.ca`; Google sends some requests to the visitor's country domain, and a CSP cannot wildcard the TLD, so each country is listed: add another, e.g. `*.google.com.au`, to both `img-src` and `connect-src` in `deploy/Caddyfile` when a console error names it). A CSP wildcard such as `*.analytics.google.com` does **not** match the bare `analytics.google.com`, nor `*.doubleclick.net` the bare host, so those are listed by name; `scripts/smoke.sh` checks for them. A browser console error "violates the following Content Security Policy directive" naming a Google host means one is missing: add that exact host, never `unsafe-inline`, HSTS (production), `Referrer-Policy: no-referrer`, `X-Content-Type-Options`, frame denial, on both Caddy and the API.
- **Google Analytics (opt-out):** on by default, a deliberate choice for a US-targeted launch (decided 2026-10-20: US privacy law is opt-out for analytics, and the owner does not want to lose data in the early months). Nothing from Google is in the HTML; `web/src/analytics.ts` adds the script from our own bundle, so the CSP needs no `unsafe-inline` or hash. A visitor can opt out from the "Cookie settings" footer link (stored in the first-party `hawkeye_analytics=on|off` cookie, 1 year; opting out deletes the `_ga*` cookies and stops sending), and a browser sending Global Privacy Control counts as opted out unless the visitor chooses otherwise. Automatic page views and Google signals/ad personalization are off; the app sends its own `page_view` per route change containing only the origin and path plus a fixed allow-list of campaign parameters (`utm_*`, `gclid`, `msclkid`, `fbclid`; `CAMPAIGN_PARAMS` in `web/src/analytics.ts`), never the rest of the query string or the fragment, because one-time tokens (`?token=`) arrive in page addresses: never add a parameter to the allow-list that can carry a secret or personal data. A `sign_up` event (method only, no email) is sent when registration succeeds, for conversion tracking.
  **OpenAI Ads measurement pixel** (conversions from ads in ChatGPT): `web/src/analytics.ts` runs OpenAI's setup snippet from code (pixel `BQzFQqkTH5HXxdU4vT171u`; the CSP allows `https://bzrcdn.openai.com` in `script-src` and `connect-src`, and `https://bzr.openai.com` in `connect-src` and `img-src`, the hosts OpenAI documents). It follows the same opt-out as Google: not loaded while analytics is off or Global Privacy Control is on, and turning analytics off calls `oaiq("consent", false)` and deletes its `__oppref` (30 days) and `__obref` (1 year) cookies. Unlike gtag, its SDK reads `location` itself, so `addressIsShareable()` keeps it from loading or measuring on any address with a fragment or a query parameter outside the campaign tags, `oppref`, `checkout` and `interval`: one-time tokens in reset, verification, invitation and unsubscribe links never reach it. Events: `page_viewed` on each route change, `registration_completed` after sign-up, and `subscription_created` (`plan_enrollment`, amount in cents, USD, `plan_id` like `pro_year`, `event_id` = the purchase's period end, the same id Google gets) when a paid subscription is first seen as confirmed. No name, email or Stripe id is sent. Set `localStorage.hawkeye_oaiq_debug = "1"` in your own browser to turn on the SDK's console logging when checking events. Changing the pixel or adding OpenAI's Conversions API means updating the CSP, `scripts/smoke.sh` and the Privacy Policy.
  **Google Ads conversion measurement:** the same gtag also configures the Ads account `AW-18527685454` (ad personalization off; no remarketing). `trackPurchase()` in `web/src/analytics.ts` sends the Ads "Purchase" conversion (`send_to` `AW-18527685454/A0aqCPXL2aMdEM6W2IJF`). `web/src/purchaseConversion.ts` decides when: returning from Checkout (`/billing?checkout=success`) only marks the purchase pending (`hawkeye_purchase_pending` in `localStorage`, dropped after 7 days); the conversion is sent the first time the subscription is seen as paid (`paid_access`, i.e. confirmed by the provider's webhook), on the billing page or, if the buyer already left, on the next page they open (`Layout` asks for the subscription only while a purchase is pending). It is sent once per paid period (a `hawkeye_conversion_<period end>` key), and renewals or existing customers never trigger it because nothing is pending. The event carries the price paid in USD (the plan's monthly price, or its yearly price for a yearly subscription) and the period-end timestamp as `transaction_id`, never a name, email or Stripe id. It obeys the same opt-out as Analytics (the `hawkeye_analytics` cookie and Global Privacy Control), and it adds the `_gcl_au` / `_gcl_aw` cookies (up to 90 days), listed in the Privacy Policy's cookie table. Google's own snippet is an inline `<script>`, which the CSP forbids, so do not paste it into the HTML. Local hosts are excluded. In the GA4 property, turn off Enhanced measurement's "Page changes based on browser history events" so Google does not record addresses itself. Changing the measurement ID or Ads account, or adding Google products, means updating the CSP in `deploy/Caddyfile`, `scripts/smoke.sh` and the Privacy Policy.
  **Revisit when EU/UK customers appear:** the EU/UK require opt-in *before* any analytics cookie or request. The policy still says the Service is offered to the EU/UK, so this is accepted risk until then. The switch back is small: make `analyticsEnabled()` return false unless `readChoice() === 'on'`, and show a first-visit banner (the cookie settings panel in `web/src/components/CookieBanner.tsx` can be reused for it). Also update the Privacy Policy and the cookie table.
- **Tokens never in logs/referrers:** Caddy redacts `?token=` and drops `Referer/Cookie/Authorization/X-Csrf-Token/Set-Cookie` from its access log; the SPA strips tokens from the address bar after reading them; verified against a running stack.
- **Error tracking:** optional Sentry; events pass through a scrubber removing emails, bearer/API tokens, secrets and sensitive headers; request bodies and cookies are never attached.
- **Account lifecycle:** change password requires the current one, signs out other sessions and emails a notice; export returns the user's own data only; deletion needs password and typed email, cancels the Stripe subscription (customer deleted) *before* removing data, and refuses while the user owns an organization with other members.
- **Email transport:** SMTP never downgrades from TLS, verifies certificates, strips CR/LF from headers, distinguishes permanent from retryable errors.
- **Startup guards:** production refuses weak secrets, http URLs, console email, plaintext SMTP/DB. Unknown `/api/v1` paths return 404 (not 401, so route existence is not probed through auth).
- **Network:** workers sit on a dedicated network; `deploy/egress-filter.sh` drops private/metadata/host traffic at the firewall as a second layer behind `safehttp` (untested on Linux; verify with `scripts/verify-egress.sh`).
- **Incident updates are public text:** an owner's update appears on every published status page that lists the monitor. It is stored and served as plain text (control and text-direction characters refused, at most 1,000 characters and 50 per incident), and the page renders it as text through React, never as HTML, so it cannot inject markup, scripts or links. Only owners can post, and the public view never says who posted.
- **Regional workers:** a regional host runs `WORKER_CHECKS_ONLY=true` and holds only `DATABASE_URL` and `SECRETS_ENCRYPTION_KEY`, never SMTP, Stripe, session or secret manager credentials. It reaches PostgreSQL through WireGuard, where the production server forwards port 5432 alone and drops everything else from the tunnel, including traffic between workers. Those two secrets still allow reading the whole database, so a regional host is as sensitive as the production server; a probe API would remove that (`docs/deployment.md`).
- **Supply chain:** `govulncheck` (through `scripts/vulncheck.py`, which holds the few accepted findings) and `npm audit` run in CI; images pinned by version, distroless runtime.

- **Contact form:** public, so it is limited per IP (burst and 5/hour, shared across API processes), has a hidden spam-trap field, strict length limits, and rejects anything but a bare email address (no header injection through name/subject/email, all text escaped in HTML). The acknowledgment email goes to an address the visitor typed, so it has its own per-address hourly budget and support still receives every accepted message. Account context in the support email comes only from the server's own session lookup, never from the request body.

See `docs/security-review.md` for the review and residual risks.

## Monitor credentials and request material (API checks)

- **Encrypted at rest:** passwords, bearer tokens and API-key values are sealed with AES-256-GCM before they are stored (`internal/secrets`), using `SECRETS_ENCRYPTION_KEY`. Every value gets a random nonce and is bound to its monitor's ID, so a value copied to another row, tampered with, or opened with the wrong key fails to decrypt. A database dump or backup alone does not reveal credentials. Production refuses to start without the key; saving a credential with no key fails rather than storing plain text.
- **Write-only:** the API never returns a credential (only `secret_set`), exports exclude it, and the worker decrypts it only in memory for the check. It is dropped from any request redirected to another host. Deleting a monitor erases its credentials, custom headers and request body at once.
- **Unreadable credential = operator error, not an outage:** if the worker cannot decrypt, it records no result and retries, so customers never get false alerts (alert `HawkEyeCredentialsUnreadable`).
- **Not encrypted:** custom headers and the request body (visible to every member of the organization and included in exports). The UI and Terms tell users to keep secrets in Authentication only.
- **State-changing methods** (POST, PUT, PATCH, DELETE) repeat on every check, so they need a verified email address and an interval of at least 5 minutes. This limits abuse of the monitor as a request cannon, but does not make it impossible: the Terms put responsibility for the requests on the customer, and the SSRF protections (public hosts, ports 80/443, no redirects to private addresses) still apply.
- **Key handling:** one key, no rotation tooling. Rotating or losing it makes stored credentials unreadable and users must re-enter them (`docs/operations.md`).

## Two-factor sign-in

- **Optional for each person; an owner may require it for their organization** (off by default). TOTP (RFC 6238: SHA-1, 30 s, 6 digits, one step
  of clock skew either way), tested against the RFC's vectors.
- **Secrets** are sealed with `SECRETS_ENCRYPTION_KEY`, bound to the user. Setting it up asks for the password again, and the new secret is kept
  aside until a correct code proves the app works.
- **Codes work once** (`users.totp_last_step`), so an intercepted code can't be replayed, even within its 30 seconds.
- **The step after the password** holds no session: only a random challenge (hashed in the database) that lasts 10 minutes and allows 5 wrong codes,
  on top of per-IP limits on both steps and the per-account login ceiling. A wrong password still gives the same answer as before.
- **Recovery codes:** 10, 80 random bits each, stored as SHA-256, single use. Using one, making new ones, and turning two-factor sign-in on or off
  all email the person.
- **Turning it off** needs the password and a current code; the owner of an organization that requires it can't.
- **Requirement:** each member without it gets 7 days (from the later of the requirement and their joining); after that the organization refuses
  their session and their API keys (`apitokens.Authenticate`, so the MCP server too) until they set it up. Ownership can't be transferred to a
  member who would be shut out.
- **Password reset doesn't bypass it:** a reset revokes sessions and keys, and the next sign-in still asks for a code.

## API keys

- **Tokens:** `hk_` plus 256 random bits; only a SHA-256 hash is stored, with a 10-character prefix for display. Shown once. Owners are emailed
  when a key is created.
- **Authority:** a key acts as one person in one organization, with that person's role read on every request; the row is deleted with the
  membership (foreign key), so leaving or removal ends a person's keys at once. `read` keys can't use unsafe methods; `write` keys also need a plan
  with API writes, checked on every write, not only at creation.
- **Reach:** keys work only on routes registered through `tokenGroup`. Account, password, billing changes, team and invitations, ownership
  transfer, alert recipients and key management are session-only, so a leaked key can't be turned into account takeover or silence alerts.
  `TestEveryRouteIsClassifiedForAPIKeys` fails when a route is added without being classified.
- **No cookie fallback, CSRF:** a request carrying an `Authorization` header authenticates with the key alone; session-only routes refuse it
  (`SESSION_REQUIRED`) instead of falling back to any cookie that came along. That is why requests with a key can skip the CSRF check on routes
  open to keys: a browser never attaches that header across sites, and there is no CORS policy that would let a page set it.
- **Lifetime:** 90 days by default, at most a year ahead, or no expiry when the person explicitly chooses it. A password reset revokes all of the
  person's keys; a password change while signed in does not.
- **Abuse:** 25 live keys per organization; 120 requests and 30 writes a minute per key (shared limiter), on top of per-IP limits; `DISABLE_API_KEYS`
  turns all keys off without affecting sessions (`docs/operations.md#api-key-abuse`).

## MCP server

- **Same authentication as the API:** `/mcp` takes API keys only (a session cookie is not accepted), through the same code as the REST API
  (`keyPrincipal`): expired, revoked and unknown keys are refused, the per-key budgets apply, and `DISABLE_API_KEYS` turns it off.
- **Same authority as the API:** every tool acts as the key's person in the key's organization, with their current role. Writes need a write key,
  a plan with API writes, and the owner role where the REST route needs it; `TestToolsRefuseWhatTheAPIRefuses` compares every write tool with
  its REST route for each kind of key. Tool writes share the key's write budget with REST writes. No tool reaches account, billing, team, alert
  destination or key settings.
- **Origins:** a request with an `Origin` header is refused unless it comes from Claude's web apps (DNS-rebinding and cross-site guard); the key is
  still required.
- **Output:** small structured results; never monitor credentials, headers, request bodies or response bodies. Names, incident summaries and
  update messages are returned as data fields, and the server's instructions tell clients they are data, not instructions (prompt injection from
  a monitored site or a team member's text). Tool descriptions say what a tool does and never tell the model how to behave otherwise.
- **Confirmation:** posting and deleting incident updates and deleting maintenance windows are marked destructive, so Claude asks before running
  them. That is a request to the client: other clients may not ask, which is why the description of `post_incident_update` says it is public.
- **No conversation data:** the server stores nothing about the conversation; logs and metrics record the tool, outcome and duration only.

## Known gaps

- Console email provider (development) logs verification/reset links; it is refused in production.
- `SESSION_SECRET` is validated but not used for signing (sessions are random DB-backed tokens); reserved.
- No audit log of security events beyond application logs.
- Refunds/disputes are surfaced as alerts only; handled manually.

## Free SSL checker (public, outbound)

`POST /api/v1/tools/ssl-check` connects to a host an anonymous visitor names, so it is built as a poor tool for abuse (`internal/sslcheck`): host names only (IP addresses, ports other than 443, private-network suffixes and credentials are refused before any network access); the connection goes through `safehttp.NewDialer`, the same destination policy as monitor checks (the name is resolved by us, every answer must be public, the validated address is dialed, so DNS rebinding and mixed records do not help); the TLS handshake is read and the connection closed, with no HTTP request and nothing returned from the host except facts about its certificate; an 8 s deadline; 8 concurrent checks; each host can be checked 5 times per 10 minutes (so the tool cannot be pointed repeatedly at one server) with a 60 s result cache; per-IP limits of 10/minute and 40/hour shared across API processes. A POST, so a page or crawler cannot trigger it, and it needs the CSRF token. The handshake verifies the certificate as a browser would (system trust store, the name asked about) and negotiates TLS 1.2 or newer; verification is never turned off. A certificate that does not verify (expired, self-signed, wrong name, unknown authority) fails the handshake, the chain the server presented comes back with that error, and the checker explains it (a test fails if `InsecureSkipVerify` or a TLS version below 1.2 appears in `internal/sslcheck/sslcheck.go`). The consequences: no protocol or cipher is shown for a certificate that does not verify, and a server that only supports TLS 1.1 or older is reported as unable to complete a secure connection. Tests: `internal/sslcheck`, `internal/httpapi/tools_integration_test.go`, and `SSLCHECK_LIVE=1` against badssl.com (run it on Linux). See `docs/security-review.md` item 12 for what is not covered.

### Website status checker ("is it down?")

`POST /api/v1/tools/site-check` (`internal/sitecheck`) has the same protections plus the ones an HTTP request needs. It reuses the monitoring engine's `checks.Checker`, so every connection and every redirect hop goes through `safehttp` (public addresses only, ports 80 and 443, at most 5 redirects, no credentials in redirects). The visitor's input is reduced to a host (`internal/publichost`, shared with the SSL checker); the request is always `GET /` with no custom headers or body, so a pasted link containing a token is never sent and the tool cannot trigger an action on a site; the response body is read only to drain it, never returned or stored; the report holds the status code, timing, the final address without its query, and a verdict. A bare domain is tried over https, then over http only if https could not connect or negotiate TLS. The User-Agent is `HawkEye-StatusChecker/1.0 (+https://hawkeye.observer/tools/is-website-down)` so site owners can identify and block it. The shared limits live in `internal/toolguard` (concurrency 8, 5 fresh checks per host per 10 minutes, 30 s cache) and each tool has its own per-IP budget (10/minute, 40/hour). Tests: `internal/sitecheck`, `internal/toolguard`, `internal/httpapi/tools_integration_test.go`, `SITECHECK_LIVE=1` (on Linux).

## DNS monitors (outbound lookups)

A DNS monitor (`internal/checks/dns.go`) sends no traffic to the customer's host: it asks the worker's own resolver for one record. The risk is learning what exists on HawkEye's network, so:

- Host names pass `publichost.ParseDNSName`: no IP addresses, single labels or private-network suffixes, and the last label may not contain an underscore, which keeps out container network names. This is input hygiene; the next two rules are the control.
- Lookups use the fully qualified name (a trailing dot), so the resolver's search domains can never turn a customer's name into an internal one.
- An `A` or `AAAA` answer that is not public fails the check as `blocked`, the same result a website gets, and an expected value must itself be a public address. A private answer therefore cannot be confirmed by guessing it.
- Answers are never stored, logged or shown: summaries name only the customer's own expected value, so a monitor cannot be used to read records out of a zone.

`MX`, `NS` and `TXT` answers are not filtered, because nothing connects to them. Tests: `internal/checks/dns_test.go`, `internal/publichost/publichost_test.go`, `internal/monitors/dns_integration_test.go`.

## TCP port monitors (policy decided, not built)

Roadmap item #12 also covers TCP port monitors. Nothing below exists yet: this is the agreed policy to build to, recorded here because it changes a deliberate defense. Today website checks reach ports 80 and 443 only (`safehttp.IsAllowedPort`), and that allowlist is the **only** limit on ports: `deploy/egress-filter.sh` drops private ranges but allows every port on the public internet. Without care, a TCP monitor turns HawkEye into a port scanner aimed at third parties.

**Decided:** a curated allowlist of ports, on paid plans only.

- **Paid plans only**, through a new plan flag decided in `internal/plans` like every other entitlement, with a verified email address required. Free accounts are the cheapest to create in bulk, so they are the likeliest source of abuse.
- **A curated allowlist, never a free-form port.** The list lives in one place in code, and adding a port is a reviewed change. Starting list, confirmed on 2026-11-05: 22 (SSH), 110, 143, 465, 587, 993, 995 (mail), 1433, 3306, 5432, 6379, 27017 (databases), 8080, 8443 (alternate web).
- **Port 25 stays blocked.** Reaching third-party mail servers from HawkEye's addresses risks them being listed for spam. Reconsider only with a specific reason.
- **Connect only.** The check opens a connection and closes it: no bytes are sent and no banner is read or stored.

**Requirements when it is built:**

- Dial through `safehttp.NewDialer` with a TCP-only `AllowPort`, so every existing rule still applies (all answers must be public, the validated address is dialed, no second lookup). Website checks keep their own 80/443 rule; the new list must not widen it.
- Limit how many distinct hosts and ports one organization can monitor, so an account cannot sweep a network within its monitor limit, and add a rate limit per destination across all organizations.
- Add a matching port rule to `deploy/egress-filter.sh`, so the network enforces the list as well and a bug in one layer is not a breach. Verify it with `scripts/verify-egress.sh`.
- Update the Terms (acceptable use) and `docs/legal-placeholders.md`, and record abuse handling in `docs/operations.md`.

## Heartbeat pings (public, inbound)

`/ping/<token>[/fail|/<exit status>]` (`internal/heartbeats`, `internal/httpapi/handlers_heartbeats.go`) is the only endpoint that records data
without a session. It is called by customers' scheduled jobs, so it sits outside `/api`, its CSRF check and its global limit, like the badges.
What protects it:

- **The token is the only authority, and all it can do is record a ping** for that one monitor. Tokens are 128 random bits (22 URL-safe
  characters, unique index), never derived from the monitor id; anything that is not 22 such characters is refused before the database is
  touched. Deleting the monitor clears the token, and "Get a new ping address" (`POST /monitors/:id/ping-token`) replaces it at once.
- **Inbound only.** A ping makes HawkEye contact nothing, so there is no SSRF surface; it cannot change settings, read data or create monitors.
- **Bounded cost.** In-memory limits per API process: about one ping a second per token (burst 30) and 20/s per IP (burst 600, as many jobs may share
  one server). Only the first 4 KiB of a POST body is read and 1,000 characters kept; user agent 200. Each ping is one short transaction, and the log keeps
  the last 100 pings per monitor.
- **Stored data.** The ping log holds the time, method, source IP, user agent and the start of the body: the customer's own data about their own
  servers, shown only to their organization, deleted with the monitor. It is never included in alerts (incident summaries are fixed sentences such as
  "The job exited with status 3"), status pages or badges.
- **No false alarms from our own outages.** The sweeper decides nothing while no API process has marked `ping_intake` recently, and gives every
  heartbeat a full period after such a gap (see `docs/api.md`, *Heartbeat monitors*).

Tests: `internal/heartbeats/heartbeats_integration_test.go`, `internal/httpapi/heartbeats_integration_test.go`.

## Settings and secrets handling

Production settings are fetched from Infisical at deploy time by `scripts/fetch-secrets.py` (standard library only; HTTPS; no value is ever printed or logged: output goes to mode-600 files, or for the nightly copy straight into age, and only counts and manifest names are printed) and written by `scripts/sync-secrets.sh` to `.env.production` and `~/.hawkeye-host.env` (mode 600, atomic replace, validated against `deploy/env.manifest` first: required values present, no placeholders, nothing that a `.env` file cannot hold). Values are written unquoted or single-quoted so that docker compose and bash read them identically (tested against both). Host-only values (backup bucket keys, the heartbeat URL) go to the host file and never to the containers' environment. The credentials file refuses to be used unless it is mode 600 and its API address is https (plain http only for localhost, for the tests). See `docs/secrets.md`.
