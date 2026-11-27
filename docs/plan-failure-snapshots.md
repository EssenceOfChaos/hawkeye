# Plan: rich failure snapshots

Status: **in progress** on branch `feat/failure-snapshots` (2026-11-27). Roadmap 2, feature #8 (Q2, about 3 build weeks). It builds on the timing breakdown (#3,
PR #84), immediate re-checks (#2), more check locations (#1) and alert grouping (#10).

When an outage is confirmed, the alert and the incident say **what HawkEye actually saw**, not only "Received HTTP 502":

- the status code, the address it ended up at, the content type and size;
- a short list of **response headers**, with anything secret left out;
- the **timing breakdown** of the failing check;
- for multi-step checks, **which step failed**;
- **what changed since the last good check**: status, address, headers, size, server address, certificate, timings;
- what **each location** saw, when the monitor checks from more than one;
- an **opt-in body excerpt** (the first 4 KB), encrypted, shown in the app only.

Why customers care: "502 from Cloudflare, the origin's server address changed 3 minutes ago" or "the response is now `text/html` instead of
`application/json`, 1.2 KB instead of 40 KB" names the fix in the alert itself. Today the customer opens a browser and reproduces the
failure, sometimes after it has gone away. Basic ping tools send a status code at most.

## How it works today

- `checks.Checker.run` (`internal/checks/checker.go`) keeps the status code, response time, timings, final URL (no query), content type
  (not stored) and a fixed, sanitized `ErrorSummary`. Headers are read only for workflow extractions and never stored. The body is read only
  when there are assertions (at most 1 MiB, `MaxBodyBytes`), otherwise drained (64 KiB), and **never stored**.
- `Store.RecordTx` (`internal/checks/store.go`) inserts the result, and on `WentDown` calls `incidents.Engine.Opened` in the same transaction
  with only the category and summary (plus "offline from 2 of 3 locations" and "confirmed by an immediate re-check").
- The dispatcher (`internal/notifications/dispatcher.go`, `load`) builds each `Alert` from the incident row; `Render`, `chatContent`,
  `buildWebhook` and PagerDuty turn it into messages. The only failure detail is `Alert.Cause` (the summary).
- Workflow runs keep `StepOutcome`s (name, status, time, timings, category, summary) in `check_results.steps`. Secrets and saved values are
  already replaced with `[hidden]` by the run's redactor.
- Regional workers record results and open incidents themselves (over the tunnel), and already hold the secrets `Box`.
- `docs/security.md` and the Privacy Policy (`LegalPages.tsx`) both say response bodies are never stored. The excerpt changes that, which is
  why it is off by default.

## The design

### What a check captures

Every HTTP check and workflow step builds a small, sanitized `ResponseFacts` when a response arrives (nothing new is read for it):

| Field | Source | Notes |
|---|---|---|
| Status, final URL, content type | already on `Result` | final URL without credentials, query or fragment, as today |
| Size | `Content-Length`, else the bytes read or drained | "at least 64 KB" when the drain limit was hit |
| Headers | `resp.Header`, **allowlist only** | see below |
| Server address | the IP `safehttp.dialContext` connected to (added to `Phases`) | always a public address (the client refuses others) |
| Certificate | the expiry already watched by `certWatch`, plus the issuer's common name | detects a certificate swap |
| Timings | already on `Result` | |

`ResponseFacts` is used twice and **stored with no check result**: once to keep the monitor's last good response, and once, on failure, for
the snapshot.

**Header allowlist, not a denylist.** A denylist misses the custom header that carries a token. Only these are kept, values cut to 200
characters, at most 20: `Server`, `Content-Type`, `Content-Length`, `Content-Encoding`, `Location` (reduced like the final URL),
`Retry-After`, `Cache-Control`, `Age`, `Via`, `X-Cache`, `CF-Cache-Status`, `CF-Ray`, `X-Served-By`, `X-Powered-By`,
`X-Request-Id`, `X-Amz-Cf-Pop`, `X-Vercel-Id`, `Fly-Request-Id`, `WWW-Authenticate` (scheme only), `X-RateLimit-*` and `RateLimit-*`.
`Set-Cookie`, `Authorization`-like headers and everything else are never kept. The list lives in one place (`checks.snapshotHeaders`) with
a test that fails if a cookie or auth header is ever added.

**Volatile headers** (`Age`, `CF-Ray`, request IDs, `X-Served-By`) are shown in the snapshot but ignored when comparing with the last good
check, so they don't show up as "changed" every time, and different edges in different locations don't either.

### The last good response

`monitors.last_good jsonb` holds the `ResponseFacts` of the latest successful check (for a workflow, one entry per step, keyed by position).
It is written **only when its fingerprint changes** (status, final URL, content type, stable headers, server address, certificate; not
timings or size), so a steady site writes it once and then rarely. The `UPDATE monitors` that every check already runs carries it, so it adds
no extra statement. Size is kept, but a change under 20% doesn't change the fingerprint.

### What changed

`checks.Compare(lastGood, failing)` lists differences, most useful first, at most 6:

> Status: 200 → 502 · Server address: 104.21.3.7 → 172.67.9.1 · `Server`: nginx → cloudflare · Content type: application/json → text/html ·
> Size: 40 KB → 1.2 KB · Server wait: 2.4 s (usually 180 ms)

Timings are compared with the 7-day averages from rollups (`usualTimings`, already used by slow alerts), not the single last good check, so a
one-off fast check doesn't make every failure look slow. With no last good response yet (a new monitor), the snapshot says so and lists no
changes.

### The snapshot

When `Opened` creates an incident, it stores the **confirming check's** snapshot (often the immediate re-check), in the same transaction:

```sql
CREATE TABLE incident_snapshots (
    incident_id     uuid PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
    checked_at      timestamptz NOT NULL,
    region          text NOT NULL,
    facts           jsonb NOT NULL,      -- ResponseFacts of the failing check, or of the failing step
    step_index      smallint,            -- workflows: the step that failed
    step_name       text,
    changes         jsonb NOT NULL DEFAULT '[]',  -- Compare's list, worked out once, at open time
    locations       jsonb,               -- per location: latest status code or error category, and when
    body_excerpt    text,                -- sealed with secrets.Box (context: the incident ID); NULL unless opted in
    body_truncated  boolean NOT NULL DEFAULT false,
    body_cleared_at timestamptz          -- set when retention removes the excerpt early
);
```

- One row per incident, so a few KB per outage, not per check: no measurable change to `check_results`.
- `checks.Incidents.Opened` takes the `Result` (instead of category and summary) so it can build the snapshot; the region and re-check
  wording stay as they are.
- `locations` comes from each effective location's latest result (one indexed query, only for multi-location monitors).
- Deleted with its incident by the existing retention job (`ON DELETE CASCADE`).
- **Transport failures** (DNS, connect, TLS, timeout) have no response, so the snapshot has the timings up to the failure, the server
  address if it got that far, the certificate if the handshake failed on it, and the changes that still apply ("server address changed").
- **Heartbeats and DNS monitors** get no snapshot: a heartbeat has no response, and DNS answers are never stored (`docs/security.md`). Their
  alerts are unchanged.

### The opt-in body excerpt

- `monitors.capture_failure_body boolean NOT NULL DEFAULT false`, set per monitor under **Alerts → Failure details**: "Keep the first 4 KB
  of the response when a check fails". Off for every existing monitor.
- Read **only from a failed check that got a response**, only when the option is on: the first 4 KB of the body that was already read for
  assertions, or read now instead of drained. Successful checks read nothing more than today.
- Text only (invalid UTF-8 is replaced with "binary response, not kept"). Before sealing, the excerpt is redacted: the monitor's own
  credentials (basic, bearer, API-key header values), its custom header values, and for workflows the run's redactor (secrets and saved
  values) all become `[hidden]`.
- Sealed with `secrets.Box` using the incident ID as context, so a sealed excerpt can't be moved to another incident.
- **Shown in the app only**, on the incident, to members of the organization, behind a "Show response" button, rendered as plain text
  (never HTML). Never put in alerts, webhooks, PagerDuty, status pages, reports, MCP, or AI incident drafts (`internal/incidentdraft`), and
  never logged. Response text is written by the monitored site, so it is data and must not reach an AI assistant as part of a prompt.
- Turning the option on or off goes in the security log.

### Where it's shown

**Email (outage alert):** the "Latest result" fact stays; a "What HawkEye saw" block follows, with the status, final address, server address,
the timing line (`DNS 12 ms · connect 31 ms · TLS 48 ms · wait 2.4 s`), the failed step, and "Changed since the last good check" as a short
list. Headers are listed in a small table, at most 10.

**Chat (Slack, Discord, Teams, Telegram):** at most 3 extra lines, each at most 240 characters, so every app's size limit holds (tested per
app, as grouping did): the timing line, the top 2 changes, and "Location: Amsterdam 502, Los Angeles 502, New York OK" when there are
several.

**Grouped alerts:** unchanged. Each monitor's line stays short; the link goes to the incidents, where the snapshots are.

**Webhooks:** `incident.snapshot` with every field above except the body excerpt. **PagerDuty:** the same in `custom_details`.

**Incidents page and monitor page:** the incident row expands (like its updates) into **What HawkEye saw**: the facts, headers, timing bar
(the existing `TimingBar`), changes, locations, and the "Show response" button when an excerpt exists.

**API and MCP:** `GET /incidents/:id` gains `snapshot` (no body). `GET /incidents/:id/snapshot/body` returns the excerpt to members, with
`Cache-Control: no-store`. MCP `get_incident` returns the snapshot without the body, so "why did my site go
down?" can be answered from Claude.

**Status pages, badges and client reports:** no change. They show uptime, not diagnostics.

### Customer choice

- The snapshot (status, allowlisted headers, timings, changes) is on for every monitor on every plan. It holds only facts about the customer's
  own site that HawkEye already sees on every check, so there is nothing to opt into.
- The body excerpt is opt-in, per monitor, and stays off unless turned on.

## Files touched

| Area | Files |
|---|---|
| Capturing | `internal/safehttp/client.go` (the server address on `Phases`), `internal/checks/checker.go` (`ResponseFacts`, the header allowlist, the excerpt), `internal/checks/workflow.go` (per-step facts, the redactor on the excerpt), new `internal/checks/snapshot.go` (`Compare`, fingerprint) |
| Storing | `migrations/000058_failure_snapshots.*`, `internal/checks/store.go` (`last_good`, the new `Opened` signature), `internal/incidents/incidents.go` (the snapshot row, sealing) |
| Alerts | `internal/notifications/dispatcher.go` (`load` joins the snapshot), `render.go`, `channels.go` (chat lines, webhook payload), `pagerduty.go` |
| Reading | `internal/incidents/query.go`, `internal/httpapi` (incident and body endpoints, the monitor option), `internal/mcpserver/tools.go` |
| Web | `IncidentsPage.tsx`, `MonitorDetailPage.tsx` (a `FailureSnapshot` component reusing `TimingBar`), `MonitorFormPage.tsx` (the option), `api.ts` |

## Feature sync

- **Pricing cards:** "Alerts show what changed: status, headers, timings" on all plans.
- **Landing page:** a sentence in the alerts feature card; a failure snapshot in the alert example if there is one.
- **Public docs:** "What HawkEye saw" in the alerts article, with the header allowlist and how the body excerpt works.
- `README.md`, `web/public/llms.txt`, `docs/api.md` (the fields and the body endpoint), `docs/architecture.md` (capture, `last_good`, the
  snapshot), `docs/operations.md` (finding a snapshot, the retention of excerpts).
- **`docs/security.md`:** replace "bodies are never stored" with the excerpt's rules (opt-in, 4 KB, redacted, sealed, app only, retention),
  and add the header allowlist to the threat table.
- **Legal:** the Privacy Policy item about API checks ("we never store what the responses contained") becomes: response headers from an
  allowlist are kept with incidents, and, only if you turn it on for a monitor, the first 4 KB of a failed response, encrypted and deleted on
  the schedule below. `docs/legal-placeholders.md` records the change.
- **Copy sync tests:** `PlanCardsInSync.test.tsx` and the landing page tests.

## Tests

- **Allowlist:** a response with `Set-Cookie`, `Authorization`, `X-Api-Key` and a custom token header keeps none of them; the allowlist test
  fails if a cookie or auth header is added; values are cut to 200 characters and at most 20 are kept.
- **Capture:** against `httptest` servers, facts are built for 2xx, 4xx and 5xx; the server address is the dialed IP; a timeout during the
  server wait keeps DNS, connect and TLS and no headers; successful checks read no more of the body than today.
- **Compare:** volatile headers never count as changes; a 10% size change doesn't, a 90% one does; timings compare with the 7-day average;
  a new monitor lists no changes; at most 6, in order.
- **`last_good`:** written on the first success, not rewritten while the fingerprint is the same, rewritten when a stable header changes.
- **Snapshot:** one row per incident from the confirming check (a re-check when there was one); workflows name the failing step; multi-location
  monitors list every location; heartbeat and DNS incidents have none; it is deleted with its incident.
- **Excerpt:** off by default for new and existing monitors; with it on, a failing response's first 4 KB is kept, the monitor's credentials
  and header values and a workflow's secrets are `[hidden]`, binary bodies are not kept, and the sealed value can't be opened with another
  incident's ID. It never appears in emails, chat, webhooks, PagerDuty, MCP, reports or AI drafts (one test per output, asserting on a marker
  string in the body).
- **Alerts:** every chat app stays inside its size limit with the longest possible snapshot; grouped messages are unchanged.
- **Web:** the panel shows facts, changes and locations; "Show response" appears only with an excerpt and renders it as text; phone width.

## Order of work (about 3 weeks)

1. Capture: `ResponseFacts`, the allowlist, the server address, per-step facts. Unit tests against local servers.
2. Compare and `last_good`: the fingerprint, the migration's `monitors` columns, the store write.
3. The snapshot: `incident_snapshots`, the new `Opened` signature, locations, retention.
4. Alerts: email, chat, webhooks, PagerDuty, with the size-limit tests.
5. The excerpt: the option (its changes in the security log), redaction, sealing, the body endpoint, the "never leaves the app" tests.
6. Web: the panel on the incidents and monitor pages, the monitor form option.
7. Feature sync and the legal and security doc changes, then a local run with `run-hawkeye` against a URL that fails on purpose (for example
   `https://httpbin.org/status/503`), with the excerpt on and off.

## Decisions (2026-11-27): the recommendations were chosen

1. **How long a body excerpt is kept: 30 days after the incident resolves.** The roadmap said "as long as the incident", which is the plan's
   history retention (up to a year on Agency). The rest of the snapshot stays with the incident. Retention clears the excerpt and sets
   `body_cleared_at`, and the panel says "response removed after 30 days".
2. **The excerpt never appears in alerts.** Email and chat go to third parties (Slack, Discord and so on) and are kept there out of our
   reach. The alert links to the incident instead.
3. **Every plan** gets snapshots and the excerpt. They cost one small row per outage.
4. **Every member** of the organization can see an excerpt, like the incident itself. Views are **not** recorded in the security log: the
   log records changes (and bulk data exports), members can already see the incident and usually reach the site, and the change that
   matters, turning the excerpt on or off, is logged.
5. **The header allowlist** is the list above. Adding to it later is easy; a header that turns out to carry secrets is not easy to take
   back.
