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
- **Secrets:** `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` come from the environment only, are validated at startup (shape, all-or-nothing, live keys refused outside production) and never logged. Provider error text is not returned to users.
- **No card data** is handled or stored; Checkout and the portal are Stripe-hosted. Stored webhook payloads (customer IDs/emails) are retained 90 days (180 for failed).
- **Residual risks:** a compromised webhook secret lets an attacker trigger *re-reads* but not choose the applied state (Stripe's API decides it), which limits the blast radius; the conflicting-second-subscription case requires manual resolution;
  fraud/dispute/refund events (`charge.dispute.created`, `charge.refunded`) are not handled yet and need a policy before live launch.

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

## Known gaps

- Email is console-only (links are logged in development). Production refuses nothing but logs a warning; a real provider is required before launch.
- `SESSION_SECRET` is validated but not yet used (sessions are random DB-backed tokens); it is reserved for signed values added later.
- No audit log, no account deletion endpoint yet.
