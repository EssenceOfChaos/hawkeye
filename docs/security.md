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
  idempotent by event ID. Defence in depth (documented, infrastructure): allowlist Stripe's IPs. It is intentionally outside CSRF/session protection because it is signature-authenticated.
- **Authorization:** only owners with a verified email can start checkout, open the portal, cancel or re-sync; every call is scoped to the caller's verified organization (tested across tenants).
- **Secrets:** (prefer a restricted `rk_` key; see `docs/billing.md` → Keys) `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` come from the environment only, are validated at startup (shape, all-or-nothing, live keys refused outside production) and never logged. Provider error text is not returned to users.
- **No card data** is handled or stored; Checkout and the portal are Stripe-hosted. Stored webhook payloads (customer IDs/emails) are retained 90 days (180 for failed).
- **Residual risks:** a compromised webhook secret lets an attacker trigger *re-reads* but not choose the applied state (Stripe's API decides it), which limits the blast radius; the conflicting-second-subscription case requires manual resolution;
  dispute/refund/fraud-warning events are surfaced (log + alert metric) but deliberately not acted on automatically: define the policy before live launch (`docs/billing.md`).

## Data retention and privacy (Phase 6)

- Stored per check: status, timings and a fixed sanitised error summary — never page content. Retention windows per plan: Free 7 days, Pro 90, Agency 365,
  applied by an hourly batched job (see `docs/architecture.md`). Monitors you delete are purged after 30 days; expired sessions and one-time tokens are removed.
- Report endpoints are bounded (≤ 366-day windows, ≤ 150 chart points, ≤ 200 history rows per page) and tenant-scoped (`TestReportEndpointsAreTenantScoped`).
- **Not yet implemented:** self-service account deletion/export (needed for GDPR-style requests) and a published privacy policy; both are Phase 8 items.

## Alerts and email abuse (Phase 5)

- An alert address other than the owner's own must be confirmed by an emailed single-use link (48 h, hashed in the database) before it receives
  anything, and adding one requires a verified account email and is rate-limited — so the product cannot be used to spam third parties.
- Maximum 10 recipients per organization; only owners change recipients (members can view).
- Email subjects/bodies are plain text built from monitor names and URLs with control characters stripped, so a malicious monitor name cannot inject headers or extra lines (`TestRenderCannotInjectHeadersOrLines`).
- Stored delivery errors are truncated and sanitised; check results never contain response bodies.
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
| Defence in depth at connect time | The default dialer's `Control` hook re-validates the actual remote address of every connection | `TestDefaultDialerControl…` |
| Redirects to internal targets | Every redirect hop dials through the same validator; redirect scheme must be http/https, no credentials, max 5 hops | `TestRedirectToInternalTargetIsBlocked`, `TestRedirectLimitAndSchemes` |
| Alternate IP spellings (`2130706433`, `0x7f.1`, `127.1`, octal) | They are treated as host names, resolved, and the resulting address is validated; also rejected earlier by monitor URL validation | `TestAlternateIPSpellingsNeverConnect` (asserts a local listener sees zero connections) |
| Port abuse (SSH, databases, internal services) | Only ports 80 and 443, checked at monitor creation, at dial time and on redirects | `TestPortsOtherThan80And443AreBlocked` |
| Proxy bypass | The transport ignores `HTTP(S)_PROXY`; a proxy would dial for us | `TestEnvironmentProxyIsIgnored` |
| Data exfiltration through responses | Bodies are never stored or interpreted (at most 64 KiB is drained); only status, timing and a fixed error summary are persisted. Raw network errors (which can contain internal IPs) are never stored | `TestProhibitedDestinationsUnderProductionPolicy` |
| Resource exhaustion | Hard per-check timeout (1–30 s, covers DNS, connect, TLS, headers and redirects), 64 KiB response header limit, bounded worker concurrency | `TestTimeout` |

Mutation checks performed while developing: removing the pre-dial validation fails six tests; honouring proxy
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
- **Headers:** CSP `default-src 'self'; script-src 'self' https://*.googletagmanager.com` (no inline scripts; the only third-party origins are the ones Google documents for Google Analytics and Google Ads conversion measurement in `script-src`, `connect-src` and `img-src`: `*.googletagmanager.com`, `*.google-analytics.com`, `*.analytics.google.com`, `*.googleadservices.com`, `*.g.doubleclick.net`, `www.google.com`), HSTS (production), `Referrer-Policy: no-referrer`, `X-Content-Type-Options`, frame denial, on both Caddy and the API.
- **Google Analytics (opt-out):** on by default, a deliberate choice for a US-targeted launch (decided 2026-10-20: US privacy law is opt-out for analytics, and the owner does not want to lose data in the early months). Nothing from Google is in the HTML; `web/src/analytics.ts` adds the script from our own bundle, so the CSP needs no `unsafe-inline` or hash. A visitor can opt out from the "Cookie settings" footer link (stored in the first-party `hawkeye_analytics=on|off` cookie, 1 year; opting out deletes the `_ga*` cookies and stops sending), and a browser sending Global Privacy Control counts as opted out unless the visitor chooses otherwise. Automatic page views and Google signals/ad personalization are off; the app sends its own `page_view` per route change containing only the origin and path plus a fixed allow-list of campaign parameters (`utm_*`, `gclid`, `msclkid`, `fbclid`; `CAMPAIGN_PARAMS` in `web/src/analytics.ts`), never the rest of the query string or the fragment, because one-time tokens (`?token=`) arrive in page addresses: never add a parameter to the allow-list that can carry a secret or personal data. A `sign_up` event (method only, no email) is sent when registration succeeds, for conversion tracking.
  **Google Ads conversion measurement:** the same gtag also configures the Ads account `AW-18527685454` (ad personalization off; no remarketing). `trackPurchase()` in `web/src/analytics.ts` sends the Ads "Purchase" conversion (`send_to` `AW-18527685454/A0aqCPXL2aMdEM6W2IJF`) from `web/src/pages/BillingPage.tsx`, only after the billing provider has confirmed the subscription (`paid_access`, not merely the return from Checkout) and only once per paid period (a `hawkeye_conversion_<period end>` key in `localStorage`). The event carries the plan's monthly price in USD and the period-end timestamp as `transaction_id`, never a name, email or Stripe id. It obeys the same opt-out as Analytics (the `hawkeye_analytics` cookie and Global Privacy Control), and it adds the `_gcl_au` / `_gcl_aw` cookies (up to 90 days), listed in the Privacy Policy's cookie table. Google's own snippet is an inline `<script>`, which the CSP forbids, so do not paste it into the HTML. Local hosts are excluded. In the GA4 property, turn off Enhanced measurement's "Page changes based on browser history events" so Google does not record addresses itself. Changing the measurement ID or Ads account, or adding Google products, means updating the CSP in `deploy/Caddyfile`, `scripts/smoke.sh` and the Privacy Policy.
  **Revisit when EU/UK customers appear:** the EU/UK require opt-in *before* any analytics cookie or request. The policy still says the Service is offered to the EU/UK, so this is accepted risk until then. The switch back is small: make `analyticsEnabled()` return false unless `readChoice() === 'on'`, and show a first-visit banner (the cookie settings panel in `web/src/components/CookieBanner.tsx` can be reused for it). Also update the Privacy Policy and the cookie table.
- **Tokens never in logs/referrers:** Caddy redacts `?token=` and drops `Referer/Cookie/Authorization/X-Csrf-Token/Set-Cookie` from its access log; the SPA strips tokens from the address bar after reading them; verified against a running stack.
- **Error tracking:** optional Sentry; events pass through a scrubber removing emails, bearer/API tokens, secrets and sensitive headers; request bodies and cookies are never attached.
- **Account lifecycle:** change password requires the current one, signs out other sessions and emails a notice; export returns the user's own data only; deletion needs password and typed email, cancels the Stripe subscription (customer deleted) *before* removing data, and refuses while the user owns an organization with other members.
- **Email transport:** SMTP never downgrades from TLS, verifies certificates, strips CR/LF from headers, distinguishes permanent from retryable errors.
- **Startup guards:** production refuses weak secrets, http URLs, console email, plaintext SMTP/DB. Unknown `/api/v1` paths return 404 (not 401, so route existence is not probed through auth).
- **Network:** workers sit on a dedicated network; `deploy/egress-filter.sh` drops private/metadata/host traffic at the firewall as a second layer behind `safehttp` (untested on Linux; verify with `scripts/verify-egress.sh`).
- **Supply chain:** `govulncheck` and `npm audit` run in CI; images pinned by version, distroless runtime.

- **Contact form:** public, so it is limited per IP (burst and 5/hour, shared across API processes), has a hidden spam-trap field, strict length limits, and rejects anything but a bare email address (no header injection through name/subject/email, all text escaped in HTML). The acknowledgement email goes to an address the visitor typed, so it has its own per-address hourly budget and support still receives every accepted message. Account context in the support email comes only from the server's own session lookup, never from the request body.

See `docs/security-review.md` for the review and residual risks.

## Monitor credentials and request material (API checks)

- **Encrypted at rest:** passwords, bearer tokens and API-key values are sealed with AES-256-GCM before they are stored (`internal/secrets`), using `SECRETS_ENCRYPTION_KEY`. Every value gets a random nonce and is bound to its monitor's ID, so a value copied to another row, tampered with, or opened with the wrong key fails to decrypt. A database dump or backup alone does not reveal credentials. Production refuses to start without the key; saving a credential with no key fails rather than storing plain text.
- **Write-only:** the API never returns a credential (only `secret_set`), exports exclude it, and the worker decrypts it only in memory for the check. It is dropped from any request redirected to another host. Deleting a monitor erases its credentials, custom headers and request body at once.
- **Unreadable credential = operator error, not an outage:** if the worker cannot decrypt, it records no result and retries, so customers never get false alerts (alert `HawkEyeCredentialsUnreadable`).
- **Not encrypted:** custom headers and the request body (visible to every member of the organization and included in exports). The UI and Terms tell users to keep secrets in Authentication only.
- **State-changing methods** (POST, PUT, PATCH, DELETE) repeat on every check, so they need a verified email address and an interval of at least 5 minutes. This limits abuse of the monitor as a request cannon, but does not make it impossible: the Terms put responsibility for the requests on the customer, and the SSRF protections (public hosts, ports 80/443, no redirects to private addresses) still apply.
- **Key handling:** one key, no rotation tooling. Rotating or losing it makes stored credentials unreadable and users must re-enter them (`docs/operations.md`).

## Known gaps

- Console email provider (development) logs verification/reset links; it is refused in production.
- `SESSION_SECRET` is validated but not used for signing (sessions are random DB-backed tokens); reserved.
- No audit log of security events beyond application logs.
- Refunds/disputes are surfaced as alerts only; handled manually.

## Free SSL checker (public, outbound)

`POST /api/v1/tools/ssl-check` connects to a host an anonymous visitor names, so it is built as a poor tool for abuse (`internal/sslcheck`): host names only (IP addresses, ports other than 443, private-network suffixes and credentials are refused before any network access); the connection goes through `safehttp.NewDialer`, the same destination policy as monitor checks (the name is resolved by us, every answer must be public, the validated address is dialed, so DNS rebinding and mixed records do not help); the TLS handshake is read and the connection closed, with no HTTP request and nothing returned from the host except facts about its certificate; an 8 s deadline; 8 concurrent checks; each host can be checked 5 times per 10 minutes (so the tool cannot be pointed repeatedly at one server) with a 60 s result cache; per-IP limits of 10/minute and 40/hour shared across API processes. A POST, so a page or crawler cannot trigger it, and it needs the CSRF token. The handshake verifies the certificate as a browser would (system trust store, the name asked about) and negotiates TLS 1.2 or newer; verification is never turned off. A certificate that does not verify (expired, self-signed, wrong name, unknown authority) fails the handshake, the chain the server presented comes back with that error, and the checker explains it (a test fails if `InsecureSkipVerify` or a TLS version below 1.2 appears in `internal/sslcheck/sslcheck.go`). The consequences: no protocol or cipher is shown for a certificate that does not verify, and a server that only supports TLS 1.1 or older is reported as unable to complete a secure connection. Tests: `internal/sslcheck`, `internal/httpapi/tools_integration_test.go`, and `SSLCHECK_LIVE=1` against badssl.com (run it on Linux). See `docs/security-review.md` item 12 for what is not covered.

### Website status checker ("is it down?")

`POST /api/v1/tools/site-check` (`internal/sitecheck`) has the same protections plus the ones an HTTP request needs. It reuses the monitoring engine's `checks.Checker`, so every connection and every redirect hop goes through `safehttp` (public addresses only, ports 80 and 443, at most 5 redirects, no credentials in redirects). The visitor's input is reduced to a host (`internal/publichost`, shared with the SSL checker); the request is always `GET /` with no custom headers or body, so a pasted link containing a token is never sent and the tool cannot trigger an action on a site; the response body is read only to drain it, never returned or stored; the report holds the status code, timing, the final address without its query, and a verdict. A bare domain is tried over https, then over http only if https could not connect or negotiate TLS. The User-Agent is `HawkEye-StatusChecker/1.0 (+https://hawkeye.observer/tools/is-website-down)` so site owners can identify and block it. The shared limits live in `internal/toolguard` (concurrency 8, 5 fresh checks per host per 10 minutes, 30 s cache) and each tool has its own per-IP budget (10/minute, 40/hour). Tests: `internal/sitecheck`, `internal/toolguard`, `internal/httpapi/tools_integration_test.go`, `SITECHECK_LIVE=1` (on Linux).

## Settings and secrets handling

Production settings are fetched from Infisical at deploy time by `scripts/fetch-secrets.py` (standard library only; HTTPS; no value is ever printed or logged: output goes to mode-600 files, or for the nightly copy straight into age, and only counts and manifest names are printed) and written by `scripts/sync-secrets.sh` to `.env.production` and `~/.hawkeye-host.env` (mode 600, atomic replace, validated against `deploy/env.manifest` first: required values present, no placeholders, nothing that a `.env` file cannot hold). Values are written unquoted or single-quoted so that docker compose and bash read them identically (tested against both). Host-only values (backup bucket keys, the heartbeat URL) go to the host file and never to the containers' environment. The credentials file refuses to be used unless it is mode 600 and its API address is https (plain http only for localhost, for the tests). See `docs/secrets.md`.
