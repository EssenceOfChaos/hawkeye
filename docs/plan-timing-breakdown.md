# Plan: timing breakdown (DNS, connect, TLS, first byte)

Status: **built** on branch `feat/timing-breakdown` (2026-11-21), not yet released. Roadmap 2, feature #3 (Q1, about 2 build weeks). It builds on #2 (immediate re-check, PR #83), and rich
failure snapshots (#8) will reuse it.

Each HTTP check will record where its time went: **DNS lookup, connect, TLS handshake and server wait** (from sending the request until the
first byte of the response comes back). The monitor page shows the breakdown, and a slow-response alert says which part got slower. Today
HawkEye records one number: the total time until the response headers arrive.

Why customers care: "slow" has different fixes. A slow DNS lookup points to the DNS provider, a slow connect to the network or a distant
server, a slow TLS handshake to certificate setup, and a long server wait to the application or its database. Basic ping tools show only the
total.

## How it works today

- `checks.Checker.run` times `client.Do` and stores it as `response_time_ms` (`internal/checks/checker.go`). The response body is read only
  when there are assertions, so the total covers the time until the headers arrive.
- The SSRF-safe client (`internal/safehttp`) **resolves the host name itself** and dials a checked IP address. Keep-alives are off, so every
  check opens a new connection, which means every check goes through every phase.
- Redirects are followed (each one is another connection), and the total covers the whole chain.
- `httptrace` is already attached for HTTPS requests to read the certificate expiry (`TLSHandshakeDone`).

## The design

### What is measured

| Phase | Measured by | Notes |
|---|---|---|
| DNS lookup | `safehttp.dialContext`, around `Resolver.LookupNetIP` | Go's `httptrace` DNS events don't fire, because the transport only ever dials an IP literal. Zero when the URL is an IP address. |
| Connect | `safehttp.dialContext`, around the dial loop | Includes failed attempts on other addresses of the same host, which is the time the customer actually waited. |
| TLS handshake | `httptrace` `TLSHandshakeStart` / `TLSHandshakeDone` | Zero for `http://`. |
| Server wait | `httptrace` `WroteRequest` to `GotFirstResponseByte` | The application's processing time, plus one network round trip. |

- **How the client reports it:** `safehttp.WithPhases(ctx, *Phases)` puts a small accumulator on the context. `dialContext` adds DNS and
  connect time to it when one is present, and `checks.run` adds the TLS and wait times from `httptrace`. The client's behavior and security
  rules don't change, and callers that don't ask for phases pay nothing.
- **Redirects:** each phase is **summed over all hops**, and the number of redirects is stored too. The four phases then add up to almost all
  of `response_time_ms`; the rest is Go's own overhead and redirect handling, usually a millisecond or two. The UI shows any remainder as
  "other" rather than hiding it, so the bar always adds up to the total.
- **Failed checks** keep whatever phases completed. A check that timed out during the TLS handshake shows DNS and connect plus a partial TLS
  time. That's useful on its own, and it's exactly what #8 needs.
- **Not measured:** DNS monitors (the lookup is the whole check, and it's already `response_time_ms`) and heartbeats (no request).
- **Multi-step checks:** each step gets its own breakdown in its `steps` entry (the JSON already stored with the result). The monitor-level
  columns stay empty for workflows, because a sum across steps would mix up different hosts.

### Storage

`check_results` is the busiest table, so the new columns are small and nullable (no default, so adding them is a metadata-only change):

```sql
ALTER TABLE check_results
    ADD COLUMN dns_ms     smallint CHECK (dns_ms >= 0),
    ADD COLUMN connect_ms smallint CHECK (connect_ms >= 0),
    ADD COLUMN tls_ms     smallint CHECK (tls_ms >= 0),
    ADD COLUMN wait_ms    smallint CHECK (wait_ms >= 0),
    ADD COLUMN redirects  smallint CHECK (redirects >= 0);
```

`smallint` is enough because the timeout is at most 30 s (30,000 ms). That's **10 bytes a row** instead of the roadmap's estimated 16. At
the capacity plan's 225 bytes a row it adds under 5%: about 1 GB a year for a full Agency account at 60 s
(`docs/plan-sub-minute-checks.md`).

The rollups need their own count, because older results and failed checks have no phases:

```sql
ALTER TABLE check_rollups
    ADD COLUMN dns_ms_sum     bigint NOT NULL DEFAULT 0,
    ADD COLUMN connect_ms_sum bigint NOT NULL DEFAULT 0,
    ADD COLUMN tls_ms_sum     bigint NOT NULL DEFAULT 0,
    ADD COLUMN wait_ms_sum    bigint NOT NULL DEFAULT 0,
    ADD COLUMN phase_count    integer NOT NULL DEFAULT 0;   -- successful, non-re-check results that have phases
```

`rollups.Source` and `insertTotals` gain the same five values, so every reader can compute averages from raw results and from rollups alike.
The existing "every reader gives the same answer with rollups" test covers it. As with response times, **averages use successful checks
only**, and re-checks are left out (#2).

### Where it's shown

- **Monitor page, under the response-time chart:** a single horizontal bar, **"Where the time goes"**, showing the average of each phase over
  the selected range, labeled with milliseconds and a share of the total. It has a table view for screen readers, like the chart. It uses the
  dataviz skill's palette and rules, and is hidden when the range has no phases (older history, DNS monitors, heartbeats).
- **Check history:** each HTTP row's response time gets a small tooltip or expandable breakdown (`DNS 12 ms · connect 31 ms · TLS 48 ms ·
  wait 410 ms`, plus "2 redirects" when there are any). Workflow steps show theirs in the step list.
- **The response-time chart stays a single line.** A stacked chart of the phases over time is a possible later addition, once we see whether
  people use the bar.

### Slow-response alerts say what got slower

When a slowdown is confirmed (`SlowStarted`), compare the slow check's phases with the monitor's averages over the previous 7 days (from
rollups, so it's a cheap query), and name the phase that accounts for most of the extra time:

> Most of the extra time is the server's wait for a first byte: 2.1 s, against 180 ms usually.

If no phase accounts for at least half of the increase, the alert says the slowdown is spread across the connection. If there's no baseline
yet (a new monitor), it says nothing. The sentence goes into the slowdown alert for every channel (`renderSlow` and the chat renderers), and
it's stored on the `slowdowns` row so the monitor page can show it too.

### API and MCP

- `GET /monitors/:id/results`: each result gains `timings: {dns_ms, connect_ms, tls_ms, wait_ms, redirects}`, or `null` when there are none.
- `GET /monitors/:id/uptime`: `avg_timings` (the same four averages), or `null` when there's no data.
- MCP `get_uptime`: the same averages, so "why is my site slow?" can be answered from Claude.
- Status pages and badges: no change. They show uptime, not diagnostics.

### Customer choice

Nothing to switch on. It's measured on every check, on every plan, and changes nothing about when alerts are sent. No new data about people
is collected (only times of the customer's own site), so the legal pages don't change.

## Files touched

| Area | Files |
|---|---|
| Measuring | `internal/safehttp/client.go` (`Phases`, `WithPhases`, timing in `dialContext`), `internal/checks/checker.go` (`httptrace` hooks, `Result.Timings`), `internal/checks/workflow.go` (per-step timings) |
| Storing | `migrations/000052_timing_breakdown.*`, `internal/checks/store.go` (insert), `internal/rollups/{job,rollups}.go` |
| Reading | `internal/reports` (results, uptime averages), `internal/httpapi`, `internal/mcpserver/tools.go` |
| Slow alerts | `internal/incidents` (`SlowStarted` works out the slow phase), `slowdowns` column, `internal/notifications/render.go` and the chat renderers |
| Web | `MonitorDetailPage.tsx` (the bar and the history breakdown), a `TimingBar` component, `api.ts` |

## Feature sync

- **Pricing cards:** "Response times split into DNS, connect, TLS and server time" on all plans. Buyers comparing against ping tools look
  for this.
- **Landing page:** a sentence in the performance feature card. The value strip's "Performance history" stays.
- **Public docs:** a "Where the time goes" section in the getting-started article, and the slow-alert explanation in the alerts article.
- `README.md`, `web/public/llms.txt`.
- `docs/api.md` (the fields), `docs/architecture.md` (how the phases are measured), `docs/plan-sub-minute-checks.md` (bytes a row).
- **Legal:** nothing new is collected.
- **Copy sync tests:** `PlanCardsInSync.test.tsx` and the landing page tests.

## Tests

- **safehttp:** with a fake resolver and a local listener, DNS and connect times are recorded only when `WithPhases` is set; an IP-literal
  URL records zero DNS; a failed first address counts toward connect; and the security rules are unchanged (the existing tests pass as
  they are).
- **Checker:** against `httptest` TLS and plain servers with deliberate delays (a handler that sleeps 200 ms), `wait_ms` is at least 200 and
  the phases add up to the total within a small margin. A redirect chain sums its hops and counts the redirects. A timeout during the server
  wait keeps DNS, connect and TLS.
- **Workflow:** each step carries its own timings.
- **Store and rollups:** the phases are stored, and every reader gives the same averages raw and rolled up. Failed checks, re-checks and
  results without phases are left out of the averages.
- **Slow alerts:** a slowdown caused by a long server wait names it; one spread across phases says so; a new monitor says nothing.
- **Web:** the bar adds up to the total and shows "other" for a remainder; it's hidden without data; there's a table view; and the history
  breakdown shows redirects.

## Order of work (about 2 weeks)

1. Measuring: `safehttp.Phases`, the `httptrace` hooks, `Result.Timings`, workflow steps. Unit tests against local servers.
2. Storing: the migration, the store insert, rollups and `Source`, with the reader-equality test.
3. Reading: the API, MCP and uptime averages.
4. Web: the bar and the history breakdown, with a check at phone width.
5. Slow alerts: the baseline query, the wording, and every channel.
6. Feature sync, then a local run with `run-hawkeye` against a deliberately slow public URL (for example `https://httpbin.org/delay/2`, which
   should show up as server wait).

## Decisions (2026-11-21): the recommendations below were chosen

1. **Merge PR #83 first.** Both add a migration and change the result insert in `store.go`. Building this on top of `main` after #83 is
   merged avoids a conflict and a renumbered migration. Recommended: merge #83, then branch.
2. **How the monitor page shows it.** Recommended: one "where the time goes" bar for the selected range, plus a per-check breakdown in the
   history, keeping the response-time chart as it is. The alternative is turning the chart into a stacked chart of the phases over time,
   which is richer but busier and heavier to load for long ranges.
3. **Naming the last phase.** Recommended: "server wait (time to first byte)". It's what a customer needs to know (their application's
   time), and "TTFB" alone usually means the cumulative time from the start, which would double-count the other phases.
